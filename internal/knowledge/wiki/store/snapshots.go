package store

import (
	"context"
	"database/sql"
	"errors"

	catalogstore "deepcode/internal/repo/catalog/store"
)

// Snapshot is what a wiki job weighs of the snapshot it writes the wiki of.
type Snapshot struct {
	ID                           int64
	Status, WikiStatus, Language string // Language: that of the pages stored so far
}

// Snapshot returns repository repoID's snapshot at commit sha, or ErrNotFound.
func (s *Store) Snapshot(ctx context.Context, repoID int64, sha string) (Snapshot, error) {
	var snap Snapshot
	err := s.DB.QueryRowContext(ctx, `SELECT id, status, wiki_status, wiki_language FROM snapshots WHERE repository_id = ? AND commit_sha = ?`,
		repoID, sha).Scan(&snap.ID, &snap.Status, &snap.WikiStatus, &snap.Language)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	return snap, err
}

// SetStatus records the wiki status of snapshot snap.
func (s *Store) SetStatus(ctx context.Context, snap int64, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE snapshots SET wiki_status = ? WHERE id = ?`, status, snap)
	return err
}

// Base returns the id and commit of repository repoID's newest indexed
// snapshot other than snap whose wiki is finished and in language, or
// ErrNotFound.
func (s *Store) Base(ctx context.Context, repoID, snap int64, language string) (id int64, sha string, err error) {
	err = s.DB.QueryRowContext(ctx, `
		SELECT id, commit_sha FROM snapshots
		WHERE repository_id = ? AND id <> ? AND status = 'ready' AND wiki_status = ? AND wiki_language = ?
		ORDER BY id DESC LIMIT 1`, repoID, snap, catalogstore.WikiReady, language).Scan(&id, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return id, sha, err
}

// Shown is the snapshot whose wiki a repository shows.
type Shown struct {
	ID                               int64
	CommitSHA, IndexedAt, WikiStatus string
}

// Shown returns the snapshot whose wiki repository repoID shows: the newest
// indexed snapshot with a finished wiki, else the published snapshot if it
// has pages, else the newest other indexed snapshot that has pages; or
// ErrNotFound. A pruned snapshot is never shown.
func (s *Store) Shown(ctx context.Context, repoID int64) (Shown, error) {
	var w Shown
	err := s.DB.QueryRowContext(ctx, `
		SELECT s.id, s.commit_sha, coalesce(s.indexed_at, ''), s.wiki_status FROM snapshots s
		WHERE s.repository_id = ?1 AND s.status = 'ready' AND EXISTS (SELECT 1 FROM wiki_pages p WHERE p.snapshot_id = s.id)
		ORDER BY s.wiki_status = ?2 DESC, s.id = (SELECT ready_snapshot_id FROM repositories WHERE id = ?1) DESC, s.id DESC
		LIMIT 1`, repoID, catalogstore.WikiReady).Scan(&w.ID, &w.CommitSHA, &w.IndexedAt, &w.WikiStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return Shown{}, ErrNotFound
	}
	return w, err
}
