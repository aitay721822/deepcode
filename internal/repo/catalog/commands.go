package catalog

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"

	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/provider"
)

// Import resolves raw (a repository URL, or a path on git server serverID,
// 0 for the first server) against the configured git servers, pins the
// repository's HEAD commit, adds the repository and queues its index job.
// onAdd, unless nil, writes in the transaction that adds the repository. A
// repository imported before is returned unchanged, with created false.
func (c *Catalog) Import(ctx context.Context, raw string, serverID int64, onAdd func(tx *sql.Tx, id int64) error) (repo Repo, created bool, err error) {
	srv, path, err := c.Servers.Resolve(ctx, raw, serverID)
	if err != nil {
		return Repo{}, false, err
	}
	var id int64
	err = c.DB.QueryRowContext(ctx, `SELECT id FROM repositories WHERE lower(url) = lower(?)`, srv.WebURL(path)).Scan(&id)
	if err == nil {
		repo, err = c.Get(ctx, id)
		return repo, false, err
	} else if !errors.Is(err, sql.ErrNoRows) {
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
		return Repo{}, false, failf(ErrUpstream, "%s answered with the repository path %q", srv.Name, meta.Path)
	}
	if meta.SizeKB > c.MaxRepoKB {
		return Repo{}, false, failf(ErrTooLarge, "%s is %d MB, over the %d MB limit (DEEPCODE_MAX_REPO_MB)",
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
func (c *Catalog) add(ctx context.Context, srv gitserver.Server, url string, meta provider.Meta, onAdd func(*sql.Tx, int64) error) (id int64, created bool, err error) {
	tx, err := c.DB.BeginTx(ctx, nil) // BEGIN IMMEDIATE: no other import lands between the check and the insert
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE lower(url) = lower(?)`, url).Scan(&id)
	if err == nil {
		return id, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	cut := strings.LastIndex(meta.Path, "/")
	// Selecting the server keeps a server deleted since it was resolved from
	// getting a repository.
	err = tx.QueryRowContext(ctx, `
		INSERT INTO repositories(url, owner, name, description, stars, default_branch, git_server_id)
		SELECT ?, ?, ?, ?, ?, ?, id FROM git_servers WHERE id = ?
		RETURNING id`, url, meta.Path[:cut], meta.Path[cut+1:], meta.Description, meta.Stars, meta.DefaultBranch, srv.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, failf(ErrInvalid, "the git server %s was removed", srv.Name)
	}
	if err == nil && onAdd != nil {
		err = onAdd(tx, id)
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, tx.Commit()
}

// Sync queues what brings repository id up to date: an index job when the
// remote HEAD moved or the embedding model changed, otherwise a wiki job when
// the wiki is missing or failed. A nil job means it is up to date. Unchanged
// files and vectors are reused by the indexer.
func (c *Catalog) Sync(ctx context.Context, id int64) (*jobs.Job, error) {
	var sha, model, wikiStatus sql.NullString
	err := c.DB.QueryRowContext(ctx, `
		SELECT s.commit_sha, s.embedding_model, s.wiki_status
		FROM repositories r LEFT JOIN snapshots s ON s.id = r.ready_snapshot_id WHERE r.id = ?`, id).Scan(&sha, &model, &wikiStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failf(ErrNotFound, "repository not found")
	} else if err != nil {
		return nil, err
	}
	remote, err := c.Servers.Remote(ctx, id)
	if err != nil {
		return nil, err
	}
	head, err := c.Git.LsRemote(ctx, remote)
	if err != nil {
		return nil, lsRemoteFailed(err)
	}
	var job jobs.Job
	switch {
	case head != sha.String || model.String != c.EmbedModel:
		job, err = c.Queue.EnqueueIndex(ctx, id, head, c.EmbedModel)
	case c.ChatModel != "" && (wikiStatus.String == WikiNone || wikiStatus.String == WikiFailed):
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
// the remote HEAD is not the published commit, unless that commit's index
// job is queued, running or failed. One that succeeded runs again: the branch
// went back to its commit. A job that failed is left to a Sync, so a
// repository too large to index is not downloaded every round; a changed
// embedding model and a failed wiki are left to a Sync too. Its errors go to
// the server log only, so git's own words are kept, unlike Sync's.
func (c *Catalog) Poll(ctx context.Context, id int64) (*jobs.Job, error) {
	remote, err := c.Servers.Remote(ctx, id)
	if err != nil {
		return nil, err
	}
	head, err := c.Git.LsRemote(ctx, remote)
	if err != nil {
		return nil, err
	}
	r, err := c.Get(ctx, id) // after the slow ls-remote, so an index that finished meanwhile counts
	if err != nil || (r.Snapshot != nil && r.Snapshot.CommitSHA == head) {
		return nil, err
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

// lsRemoteFailed tells the user why the repository's HEAD could not be
// read: an empty repository as such; otherwise only that git failed, since
// git's own words may quote paths or hosts, and those go to the log.
func lsRemoteFailed(err error) error {
	if errors.Is(err, git.ErrEmpty) {
		return kinded{ErrInvalid, err}
	}
	log.Printf("catalog: %v", err)
	return failf(ErrUpstream, "git could not read the repository from its server; an administrator can find the details in the server log")
}
