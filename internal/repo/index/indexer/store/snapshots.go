// Package store keeps the index in SQLite: each repository's snapshots, and
// each snapshot's files, symbols and chunks, with the vectors they reuse.
package store

import (
	"context"
	"database/sql"
	"errors"
)

// Store runs the indexer's SQL.
type Store struct{ DB *sql.DB }

// Published returns the snapshot repository repoID has published, 0 for none.
func (s *Store) Published(ctx context.Context, repoID int64) (int64, error) {
	var prev sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT ready_snapshot_id FROM repositories WHERE id = ?`, repoID).Scan(&prev)
	return prev.Int64, err
}

// Pruned returns the pruned snapshot of repoID at sha; ok is false when there
// is none.
func (s *Store) Pruned(ctx context.Context, repoID int64, sha string) (id int64, ok bool, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM snapshots WHERE repository_id = ? AND commit_sha = ? AND status = 'pruned'`,
		repoID, sha).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// Begin creates the snapshot of sha, read by analyzer version, or takes up
// the one there is: a ready one stays ready, any other is indexing again. It
// returns the snapshot, its status and the analyzer version that read it.
func (s *Store) Begin(ctx context.Context, repoID int64, sha string, version int) (snap int64, status string, read int, err error) {
	err = s.DB.QueryRowContext(ctx, `
		INSERT INTO snapshots(repository_id, commit_sha, analyzer) VALUES (?, ?, ?)
		ON CONFLICT(repository_id, commit_sha) DO UPDATE SET status = CASE status WHEN 'ready' THEN 'ready' ELSE 'indexing' END
		RETURNING id, status, analyzer`, repoID, sha, version).Scan(&snap, &status, &read)
	return snap, status, read, err
}

// Indexing marks snapshot snap as being indexed by analyzer version.
func (s *Store) Indexing(ctx context.Context, snap int64, version int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE snapshots SET status = 'indexing', analyzer = ? WHERE id = ?`, version, snap)
	return err
}

// Publish marks the snapshot READY, embedded by model and read by analyzer
// version, and makes it the repository's current version in one transaction,
// so readers switch atomically.
func (s *Store) Publish(ctx context.Context, repoID, snap int64, model string, version int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE snapshots SET status = 'ready', indexed_at = CURRENT_TIMESTAMP, embedding_model = ?1, analyzer = ?3,
			file_count = (SELECT count(*) FROM source_files WHERE snapshot_id = ?2),
			chunk_count = (SELECT count(*) FROM chunks WHERE snapshot_id = ?2)
		WHERE id = ?2`, model, snap, version); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE repositories SET ready_snapshot_id = ? WHERE id = ?`, snap, repoID); err != nil {
		return err
	}
	return tx.Commit()
}

// Stale returns the snapshots of repoID that pruning empties: all but current
// and the newest other ready one, those left half emptied included.
func (s *Store) Stale(ctx context.Context, repoID, current int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id FROM snapshots s WHERE repository_id = ?1
			AND (status <> 'pruned' OR EXISTS (SELECT 1 FROM source_files f WHERE f.snapshot_id = s.id))
			AND id NOT IN (
				SELECT id FROM snapshots WHERE repository_id = ?1 AND status = 'ready'
				ORDER BY id = ?2 DESC, indexed_at DESC, id DESC LIMIT 2)`, repoID, current)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		stale = append(stale, id)
	}
	return stale, rows.Err()
}

// MarkPruned marks snapshot id pruned, so readers leave it at once.
func (s *Store) MarkPruned(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE snapshots SET status = 'pruned' WHERE id = ?`, id)
	return err
}

// DropRows deletes snapshot id's index rows (vectors follow chunks by
// cascade); source files go last, which is how Stale tells a snapshot that
// still holds data.
func (s *Store) DropRows(ctx context.Context, id int64) error {
	for _, table := range []string{"chunks", "symbols", "source_files"} {
		if err := s.deleteInBatches(ctx, `DELETE FROM `+table+` WHERE id IN (SELECT id FROM `+table+` WHERE snapshot_id = ? LIMIT 1000)`, id); err != nil {
			return err
		}
	}
	return nil
}

// DropUnusedContents deletes the file contents no snapshot uses any more.
func (s *Store) DropUnusedContents(ctx context.Context) error {
	return s.deleteInBatches(ctx, `DELETE FROM file_contents WHERE hash IN (
		SELECT hash FROM file_contents WHERE hash NOT IN (SELECT content_hash FROM source_files) LIMIT 1000)`)
}

// deleteInBatches repeats a DELETE of at most 1000 rows until it deletes
// none, so no single statement holds the database's write lock for long.
func (s *Store) deleteInBatches(ctx context.Context, query string, args ...any) error {
	for {
		res, err := s.DB.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
	}
}
