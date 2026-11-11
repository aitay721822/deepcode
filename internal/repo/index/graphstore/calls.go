package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"deepcode/internal/platform/storage"
)

// UnreadReferences passes read the hash, a language and the content of up to
// limit of the snapshot's contents whose calls were never read, those past
// hash after, in hash order.
func (s *Store) UnreadReferences(ctx context.Context, snap int64, langs []string, after string, limit int, read func(hash, lang, content string)) error {
	cond, args := grammars(langs)
	rows, err := s.DB.QueryContext(ctx, `SELECT c.hash, min(f.language), c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND c.calls IS NULL AND c.hash > ? AND NOT f.generated AND `+cond+`
		GROUP BY c.hash ORDER BY c.hash LIMIT ?`, append(append([]any{snap, after}, args...), limit)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hash, lang, content string
		if err := rows.Scan(&hash, &lang, &content); err != nil {
			return err
		}
		read(hash, lang, content)
	}
	return rows.Err()
}

// References is what a content calls, as a file stores it.
type References struct{ Hash, References string }

// SetReferences stores the calls of a batch of contents in one transaction.
func (s *Store) SetReferences(ctx context.Context, batch []References) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, r := range batch {
		if _, err := tx.ExecContext(ctx, `UPDATE file_contents SET calls = ? WHERE hash = ?`, r.References, r.Hash); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Call is one call edge: src calls a name declared at line dstLine of dst.
type Call struct {
	Src, Dst, Name   string
	SrcLine, DstLine int
}

// Callables returns each callable name the snapshot declares, with the file
// and line of each declaration: a name declared once (or once as a callable)
// is what a name-based edge needs.
func (s *Store) Callables(ctx context.Context, snap int64) (map[string][]Call, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.name, s.path, s.start_line FROM symbols s
		JOIN source_files f ON f.snapshot_id = s.snapshot_id AND f.path = s.path
		WHERE s.snapshot_id = ? AND s.kind IN ('function', 'method', 'constructor', 'class') AND NOT f.generated`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Call{}
	for rows.Next() {
		var name, path string
		var line int
		if err := rows.Scan(&name, &path, &line); err != nil {
			return nil, err
		}
		out[name] = append(out[name], Call{Dst: path, DstLine: line})
	}
	return out, nil
}

// ReplaceCalls replaces the snapshot's call edges in one transaction, unless
// the snapshot was pruned or deleted meanwhile.
func (s *Store) ReplaceCalls(ctx context.Context, snap int64, calls []Call) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snap).Scan(&status); errors.Is(err, sql.ErrNoRows) || status == "pruned" {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM call_edges WHERE snapshot_id = ?`, snap); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO call_edges(snapshot_id, src, dst, name, src_line, dst_line) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range calls {
		if _, err := stmt.ExecContext(ctx, snap, c.Src, c.Dst, c.Name, c.SrcLine, c.DstLine); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UnlinkCalls deletes the snapshot's call edges, batch at a time so no
// statement holds the write lock for long.
func (s *Store) UnlinkCalls(ctx context.Context, snap int64, batch int) error {
	for {
		res, err := s.DB.ExecContext(ctx, `DELETE FROM call_edges WHERE snapshot_id = ?1 AND (src, dst, name, src_line) IN
			(SELECT src, dst, name, src_line FROM call_edges WHERE snapshot_id = ?1 LIMIT ?2)`, snap, batch)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n < int64(batch) {
			return err
		}
	}
}

// CallersOf returns the files that call a name declared in one of dsts, sorted.
func (s *Store) CallersOf(ctx context.Context, snap int64, dsts []string) ([]string, error) {
	if len(dsts) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(dsts)+1)
	args = append(args, snap)
	for _, d := range dsts {
		args = append(args, d)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT src FROM call_edges WHERE snapshot_id = ? AND dst IN (`+
		storage.Placeholders(len(dsts))+`) ORDER BY src`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out, rows.Err()
}
