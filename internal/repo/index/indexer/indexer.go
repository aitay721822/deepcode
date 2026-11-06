// Package indexer turns one pinned commit into a READY snapshot: files, symbols,
// symbol-aware chunks (with FTS5 kept in sync by triggers) and embeddings.
package indexer

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/index/vectorstore"
)

type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

type Indexer struct {
	DB      *sql.DB
	Git     *git.Client
	Servers *gitserver.Store // where each repository is fetched from
	Embed   *ai.OpenAI       // nil: FTS5 and symbol search only
	Vectors *vectorstore.SQLite
	Events  *events.Bus // receives snapshot.published
	Limits  Limits
}

// Run indexes repoID at sha, publishes the snapshot and announces it with
// snapshot.published. It is safe to re-run: files already committed for this
// snapshot are skipped, files unchanged since the previous READY snapshot are
// copied, and vectors are reused by content.
func (ix *Indexer) Run(ctx context.Context, repoID int64, sha string, progress jobs.Progress) error {
	var prev sql.NullInt64
	if err := ix.DB.QueryRowContext(ctx, `SELECT ready_snapshot_id FROM repositories WHERE id = ?`, repoID).Scan(&prev); err != nil {
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
	var pruned int64
	switch err := ix.DB.QueryRowContext(ctx, `SELECT id FROM snapshots WHERE repository_id = ? AND commit_sha = ? AND status = 'pruned'`,
		repoID, sha).Scan(&pruned); {
	case err == nil:
		if err := ix.dropSnapshotData(ctx, pruned); err != nil {
			return err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	var snap int64
	var status string
	err = ix.DB.QueryRowContext(ctx, `
		INSERT INTO snapshots(repository_id, commit_sha) VALUES (?, ?)
		ON CONFLICT(repository_id, commit_sha) DO UPDATE SET status = CASE status WHEN 'ready' THEN 'ready' ELSE 'indexing' END
		RETURNING id, status`, repoID, sha).Scan(&snap, &status)
	if err != nil {
		return err
	}
	if status != "ready" {
		if err := ix.parse(ctx, dir, sha, snap, prev.Int64, progress); err != nil {
			return err
		}
	}
	model := ""
	if ix.Embed != nil {
		model = ix.Embed.ModelName
		if err := ix.embed(ctx, snap, progress); err != nil {
			return err
		}
	}
	progress.Stage("publish", 0, 0)
	if err := ix.publish(ctx, repoID, snap, model); err != nil {
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

// publish marks the snapshot READY and makes it the repository's current
// version in one transaction, so readers switch atomically.
func (ix *Indexer) publish(ctx context.Context, repoID, snap int64, model string) error {
	tx, err := ix.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE snapshots SET status = 'ready', indexed_at = CURRENT_TIMESTAMP, embedding_model = ?1,
			file_count = (SELECT count(*) FROM source_files WHERE snapshot_id = ?2),
			chunk_count = (SELECT count(*) FROM chunks WHERE snapshot_id = ?2)
		WHERE id = ?2`, model, snap); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE repositories SET ready_snapshot_id = ? WHERE id = ?`, snap, repoID); err != nil {
		return err
	}
	return tx.Commit()
}
