// Package indexer turns one pinned commit into a READY snapshot: files, symbols,
// symbol-aware chunks (with FTS5 kept in sync by triggers), the import graph
// and embeddings.
package indexer

import (
	"context"
	"errors"
	"log"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/graph"
	"deepcode/internal/repo/index/indexer/store"
	"deepcode/internal/repo/index/indexer/vectorstore"
)

type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

type Indexer struct {
	Store   *store.Store
	Graph   *graph.Graph // the snapshots' imports
	Git     *git.Client
	Servers *gitserver.Service // where each repository is fetched from
	Embed   *ai.OpenAI         // nil: FTS5 and symbol search only
	Vectors *vectorstore.SQLite
	Events  *events.Bus // receives snapshot.published
	Limits  Limits
}

// Run indexes repoID at sha, publishes the snapshot and announces it with
// snapshot.published. It is safe to re-run: files already committed for this
// snapshot are skipped, files unchanged since the previous READY snapshot are
// copied, and vectors are reused by content.
func (ix *Indexer) Run(ctx context.Context, repoID int64, sha string, progress jobs.Progress) error {
	prev, err := ix.Store.Published(ctx, repoID)
	if err != nil {
		return err
	}
	remote, err := ix.Servers.Remote(ctx, repoID)
	if errors.Is(err, gitserver.ErrSealed) {
		return jobs.Permanent(err) // no retry decrypts it; once the token is entered again, Sync queues the job anew
	} else if err != nil {
		return err
	}
	progress.Stage("fetch", 0, 0)
	progress.Logf("fetching commit %.7s from %s", sha, remote.URL)
	dir, err := ix.Git.Fetch(ctx, repoID, remote, sha)
	if errors.Is(err, git.ErrTooLarge) {
		return jobs.Permanent(err) // a retry downloads it again; once the limit is raised, Sync queues the job anew
	} else if err != nil {
		return err
	}
	// A pruned snapshot of this commit (the branch went back to it) is
	// indexed again from scratch: first empty it, in case its pruning
	// stopped midway, since parse skips the files a snapshot already holds.
	if pruned, ok, err := ix.Store.Pruned(ctx, repoID, sha); err != nil {
		return err
	} else if ok {
		if err := ix.dropSnapshotData(ctx, pruned); err != nil {
			return err
		}
	}
	snap, status, err := ix.begin(ctx, repoID, sha)
	if err != nil {
		return err
	}
	if status != "ready" {
		if err := ix.parse(ctx, dir, sha, snap, prev, status == "outdated", progress); err != nil {
			return err
		}
		// After every file is in: an import names files of any batch.
		n, err := ix.Graph.Link(ctx, snap)
		if err != nil {
			return err
		}
		progress.Logf("linked the files by %d imports", n)
	}
	model := ""
	if ix.Embed != nil {
		model = ix.Embed.ModelName
		if err := ix.embed(ctx, snap, progress); err != nil {
			return err
		}
	}
	progress.Stage("publish", 0, 0)
	// One transaction makes the snapshot READY and the repository's current
	// version, so readers switch atomically.
	if err := ix.Store.Publish(ctx, repoID, snap, model, analyzer.Version); err != nil {
		return err
	}
	progress.Logf("published snapshot #%d", snap)
	if err := ix.prune(ctx, repoID, snap); err != nil { // the snapshot is live; the next prune finishes the rest
		log.Printf("indexer: prune repository %d: %v", repoID, err)
		progress.Logf("pruning older snapshots failed; the next index retries: %v", err)
	}
	if err := ix.Events.Publish(events.TopicSnapshotPublished, events.SnapshotPublished{RepoID: repoID, CommitSHA: sha}); err != nil {
		log.Printf("indexer: announce snapshot %d: %v", snap, err) // the snapshot is live; only its follow-up work waits for a sync
	}
	return nil
}

// begin creates the snapshot of sha, or takes up the one there is, and
// returns it with its status. One an older analyzer read is not taken as it
// is: a published one is "outdated", read again in place while it serves;
// one begun and stopped midway starts again empty, since parse keeps the rows
// a snapshot holds.
func (ix *Indexer) begin(ctx context.Context, repoID int64, sha string) (int64, string, error) {
	snap, status, read, err := ix.Store.Begin(ctx, repoID, sha, analyzer.Version)
	switch {
	case err != nil || read >= analyzer.Version: // a newer one's rows (a binary rolled back) are taken as they are
		return snap, status, err
	case status == "ready":
		return snap, "outdated", nil
	}
	if err := ix.dropSnapshotData(ctx, snap); err != nil {
		return 0, "", err
	}
	return snap, "indexing", ix.Store.Indexing(ctx, snap, analyzer.Version)
}
