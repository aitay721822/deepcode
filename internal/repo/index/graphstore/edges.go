package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

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
// each file's in an order that moving them within the file keeps: a function
// by its signature, a type by the text it spans, so a field or a method that
// changes reaches it; a class spans its methods' bodies too.
func (s *Store) Declarations(ctx context.Context, snap int64, paths map[string]bool) (map[string][]string, error) {
	if len(paths) == 0 {
		return map[string][]string{}, nil
	}
	args := []any{snap}
	for p := range paths {
		args = append(args, p)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT f.path, c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND f.path IN (`+storage.Placeholders(len(paths))+`)`, args...)
	if err != nil {
		return nil, err
	}
	texts := map[string]string{}
	for rows.Next() {
		var p, text string
		if err := rows.Scan(&p, &text); err != nil {
			rows.Close()
			return nil, err
		}
		texts[p] = text
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT path, kind, qualified_name, signature, start_line, end_line FROM symbols
		WHERE snapshot_id = ? ORDER BY path`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := map[string][]string{}
	for rows.Next() {
		var p, kind, qual, sig string
		var start, end int
		if err := rows.Scan(&p, &kind, &qual, &sig, &start, &end); err != nil {
			return nil, err
		}
		if paths[p] {
			parts[p] = append(parts[p], declaration(kind, qual, sig, texts[p], start, end))
		}
	}
	for _, ds := range parts {
		slices.Sort(ds)
	}
	return parts, rows.Err()
}

// types are the declaration kinds a change of body is a change of the
// declaration: a type's fields, methods or variants are what its users use.
var types = map[string]bool{"annotation": true, "class": true, "delegate": true, "enum": true, "interface": true,
	"record": true, "struct": true, "trait": true, "type": true, "union": true}

// declaration is a symbol's fingerprint: a type's text from its first to its
// last line, the line numbers left out so moving it keeps it, or a function's
// signature; the signature when the text is gone.
func declaration(kind, qual, sig, content string, start, end int) string {
	if types[kind] && content != "" {
		if body, ok := declarationText(content, start, end); ok {
			sum := sha256.Sum256([]byte(body))
			return kind + " " + qual + " sha256:" + hex.EncodeToString(sum[:])
		}
	}
	return kind + " " + qual + " " + sig
}

// declarationText is content's lines start to end, both counted from 1 and
// included; false when the range is past them.
func declarationText(content string, start, end int) (string, bool) {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if start < 1 || start > len(lines) || end < start {
		return "", false
	}
	return strings.Join(lines[start-1:min(end, len(lines))], "\n"), true
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
