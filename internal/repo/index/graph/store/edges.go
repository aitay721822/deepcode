package store

import (
	"context"
	"database/sql"
	"errors"

	"deepcode/internal/platform/storage"
)

// Edge is an import of one file by another: Src imports Dst.
type Edge struct{ Src, Dst string }

// Edges returns all of the snapshot's edges.
func (s *Store) Edges(ctx context.Context, snap int64) ([]Edge, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT src, dst FROM file_edges WHERE snapshot_id = ?`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Src, &e.Dst); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Swap reports whether the snapshot is served and has edges, which a new
// graph then replaces in one transaction; ok is false when it is gone.
func (s *Store) Swap(ctx context.Context, snap int64) (swap, ok bool, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT status = 'ready' AND EXISTS (SELECT 1 FROM file_edges WHERE snapshot_id = ?1)
		FROM snapshots WHERE id = ?1`, snap).Scan(&swap)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return swap, err == nil, err
}

// Unlink deletes the snapshot's edges, batch at a time so no statement holds
// the write lock for long.
func (s *Store) Unlink(ctx context.Context, snap int64, batch int) error {
	for {
		res, err := s.DB.ExecContext(ctx, `DELETE FROM file_edges WHERE snapshot_id = ?1 AND (src, dst) IN
			(SELECT src, dst FROM file_edges WHERE snapshot_id = ?1 LIMIT ?2)`, snap, batch)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n < int64(batch) {
			return err
		}
	}
}

// Insert adds a batch of edges to the snapshot in one transaction, or reports
// a snapshot pruned or deleted (with its repository) meanwhile.
func (s *Store) Insert(ctx context.Context, snap int64, batch []Edge) (pruned bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snap).Scan(&status); errors.Is(err, sql.ErrNoRows) || status == "pruned" {
		return true, nil
	} else if err != nil {
		return false, err
	}
	if err := insertEdges(ctx, tx, snap, batch); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// Replace swaps the snapshot's edges for edges in one transaction, unless the
// snapshot was pruned or deleted meanwhile. It returns how many it stored.
func (s *Store) Replace(ctx context.Context, snap int64, edges []Edge) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snap).Scan(&status); errors.Is(err, sql.ErrNoRows) || status == "pruned" {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM file_edges WHERE snapshot_id = ?`, snap); err != nil {
		return 0, err
	}
	if err := insertEdges(ctx, tx, snap, edges); err != nil {
		return 0, err
	}
	return len(edges), tx.Commit()
}

// insertEdges adds edges to the snapshot; one there already (a link at start
// meeting an index job's) is kept.
func insertEdges(ctx context.Context, tx *sql.Tx, snap int64, edges []Edge) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO file_edges(snapshot_id, src, dst) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range edges {
		if _, err := stmt.ExecContext(ctx, snap, e.Src, e.Dst); err != nil {
			return err
		}
	}
	return nil
}

// Dependencies returns up to limit files that paths import, paths themselves
// aside, those more of them import first.
func (s *Store) Dependencies(ctx context.Context, snap int64, paths []string, limit int) ([]string, error) {
	in := storage.Placeholders(len(paths))
	args := []any{snap}
	for range 2 {
		for _, p := range paths {
			args = append(args, p)
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT dst FROM file_edges WHERE snapshot_id = ? AND src IN (`+in+`) AND dst NOT IN (`+in+`)
		GROUP BY dst ORDER BY count(*) DESC, dst LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPaths(rows)
}

// Declarations returns the declarations of the snapshot's files in paths,
// each file's in an order that moving them within the file keeps.
func (s *Store) Declarations(ctx context.Context, snap int64, paths map[string]bool) (map[string][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path, kind || ' ' || qualified_name || ' ' || signature AS d FROM symbols
		WHERE snapshot_id = ? ORDER BY path, d`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := map[string][]string{}
	for rows.Next() {
		var p, d string
		if err := rows.Scan(&p, &d); err != nil {
			return nil, err
		}
		if paths[p] {
			parts[p] = append(parts[p], d)
		}
	}
	return parts, rows.Err()
}

// Paths returns the paths of the snapshot's files.
func (s *Store) Paths(ctx context.Context, snap int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path FROM source_files WHERE snapshot_id = ?`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPaths(rows)
}

// HandWritten returns the paths of the snapshot's files that are not generated.
func (s *Store) HandWritten(ctx context.Context, snap int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path FROM source_files WHERE snapshot_id = ? AND NOT generated`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPaths(rows)
}

// scanPaths reads the paths that a query of one text column returned.
func scanPaths(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
