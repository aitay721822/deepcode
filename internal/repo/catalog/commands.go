package catalog

import (
	"context"
	"errors"
	"log"
	"strings"

	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/provider"
)

// OnAdd writes in the transaction that adds a repository.
type OnAdd = store.OnAdd

// Import resolves raw (a repository URL, or a path on git server serverID,
// 0 for the first server) against the configured git servers, pins the
// repository's HEAD commit, adds the repository and queues its index job.
// onAdd, unless nil, writes in the transaction that adds the repository. A
// repository imported before is returned unchanged, with created false.
func (c *Catalog) Import(ctx context.Context, raw string, serverID int64, onAdd OnAdd) (repo Repo, created bool, err error) {
	srv, path, err := c.Servers.Resolve(ctx, raw, serverID)
	if err != nil {
		return Repo{}, false, err
	}
	id, err := c.Store.RepoID(ctx, srv.WebURL(path))
	if err == nil {
		repo, err = c.Get(ctx, id)
		return repo, false, err
	} else if !errors.Is(err, ErrNotFound) {
		return Repo{}, false, err
	}
	meta, err := provider.Fetch(ctx, srv, path)
	switch {
	case errors.Is(err, provider.ErrNotFound):
		return Repo{}, false, kinded{ErrInvalid, err}
	case errors.Is(err, provider.ErrUpstream):
		return Repo{}, false, kinded{ErrUpstream, err}
	case err != nil:
		return Repo{}, false, err
	}
	// The server names the canonical path (a renamed repository's new name);
	// it is held to the rules of a typed path.
	if !gitserver.ValidPath(srv.Kind, meta.Path) {
		return Repo{}, false, failf(ErrUpstream, "%s 回傳了無效的 Repository 路徑 %q", srv.Name, meta.Path)
	}
	if meta.SizeKB > c.MaxRepoKB {
		return Repo{}, false, failf(ErrTooLarge, "%s 有 %d MB，超過 %d MB 的上限（DEEPCODE_MAX_REPO_MB）",
			meta.Path, meta.SizeKB>>10, c.MaxRepoKB>>10)
	}
	url := srv.WebURL(meta.Path)
	sha, err := c.Git.LsRemote(ctx, srv.Remote(url))
	if err != nil {
		return Repo{}, false, lsRemoteFailed(err)
	}
	if id, created, err = c.add(ctx, srv, url, meta, onAdd); err != nil {
		return Repo{}, false, err
	}
	if created {
		if _, err := c.Queue.EnqueueIndex(ctx, id, sha, c.EmbedModel); err != nil {
			return Repo{}, false, err
		}
	}
	repo, err = c.Get(ctx, id)
	return repo, created, err
}

// add inserts the repository at url on srv and runs onAdd, in one
// transaction. A repository already at url (imported meanwhile, or under the
// name the user typed before a rename) is returned with created false, and
// onAdd does not run for it.
func (c *Catalog) add(ctx context.Context, srv gitserver.Server, url string, meta provider.Meta, onAdd OnAdd) (id int64, created bool, err error) {
	cut := strings.LastIndex(meta.Path, "/")
	id, created, err = c.Store.Add(ctx, store.NewRepo{URL: url, Owner: meta.Path[:cut], Name: meta.Path[cut+1:], Description: meta.Description,
		Stars: meta.Stars, DefaultBranch: meta.DefaultBranch, ServerID: srv.ID}, onAdd)
	if errors.Is(err, ErrNotFound) { // the server was deleted since it was resolved
		return 0, false, failf(ErrInvalid, "Git 伺服器 %s 已被移除", srv.Name)
	}
	return id, created, err
}

// Sync queues what brings repository id up to date: an index job when the
// remote HEAD moved, the embedding model changed or an older analyzer read
// the published commit, otherwise a wiki job when the wiki is not ready. A
// nil job means it is up to date. Unchanged files and vectors are
// reused by the indexer.
func (c *Catalog) Sync(ctx context.Context, id int64) (*jobs.Job, error) {
	pub, err := c.Store.Published(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, failf(ErrNotFound, "找不到 Repository")
	} else if err != nil {
		return nil, err
	}
	remote, err := c.Servers.Remote(ctx, id)
	if err != nil {
		return nil, err
	}
	branch, err := c.Tracked(ctx, id)
	if err != nil {
		return nil, err
	}
	head, err := c.head(ctx, remote, branch)
	if err != nil {
		return nil, lsRemoteFailed(err)
	}
	var job jobs.Job
	switch {
	case head != pub.CommitSHA || pub.EmbeddingModel != c.EmbedModel || pub.Analyzer < analyzer.Version:
		job, err = c.Queue.EnqueueIndex(ctx, id, head, c.EmbedModel)
	case c.ChatModel != "" && pub.WikiStatus != WikiReady:
		// Generating too: a wiki job that ended after a restart (stopped, or failed by Recover) left it so; one
		// still queued or running is returned as it is.
		job, err = c.Queue.EnqueueWiki(ctx, id, head)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// Poll is the poller's Sync: it queues the index job of repository id when
// the remote HEAD is not the published commit, or is but an older analyzer
// read it, unless that commit's index job is queued, running or failed. One
// that succeeded runs again: the branch went back to its commit, or the
// analyzer is new. A job that failed is left to a Sync, so a
// repository too large to index is not downloaded every round; a changed
// embedding model and a failed wiki are left to a Sync too. Its errors go to
// the server log only, so git's own words are kept, unlike Sync's.
func (c *Catalog) Poll(ctx context.Context, id int64) (*jobs.Job, error) {
	remote, err := c.Servers.Remote(ctx, id)
	if err != nil {
		return nil, err
	}
	branch, err := c.Tracked(ctx, id)
	if err != nil {
		return nil, err
	}
	head, err := c.head(ctx, remote, branch)
	if err != nil {
		return nil, err
	}
	r, err := c.Get(ctx, id) // after the slow ls-remote, so an index that finished meanwhile counts
	if err != nil {
		return nil, err
	}
	if r.Snapshot != nil && r.Snapshot.CommitSHA == head {
		if read, err := c.Store.Analyzer(ctx, r.Snapshot.ID); err != nil || read >= analyzer.Version {
			return nil, err
		}
	}
	switch j, err := c.Queue.IndexJob(ctx, id, head, c.EmbedModel); {
	case errors.Is(err, jobs.ErrNotFound):
	case err != nil:
		return nil, err
	case j.Status != jobs.StatusSucceeded:
		return nil, nil
	}
	job, err := c.Queue.EnqueueIndex(ctx, id, head, c.EmbedModel)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// head is the commit the remote's branch points at; "" is its HEAD.
func (c *Catalog) head(ctx context.Context, remote git.Remote, branch string) (string, error) {
	if branch == "" {
		return c.Git.LsRemote(ctx, remote)
	}
	return c.Git.LsRemoteRef(ctx, remote, branch)
}

// lsRemoteFailed tells the user why the repository's HEAD could not be
// read: an empty repository as such; otherwise only that git failed, since
// git's own words may quote paths or hosts, and those go to the log.
func lsRemoteFailed(err error) error {
	if errors.Is(err, git.ErrEmpty) {
		return kinded{ErrInvalid, err}
	}
	if errors.Is(err, git.ErrNoBranch) {
		return failf(ErrInvalid, "設定的 Branch 不存在，請重新選擇")
	}
	log.Printf("catalog: %v", err)
	return failf(ErrUpstream, "Git 無法從伺服器讀取這個 Repository；管理員可在伺服器記錄檔中查看詳細資訊")
}
