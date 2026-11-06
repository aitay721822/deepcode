// The snapshot's rows in SQLite: batches committed while parsing, and the
// data of older snapshots pruned once a newer one is published.

package indexer

import (
	"context"
	"database/sql"

	"deepcode/internal/repo/index/analyzer"
)

// write commits one batch in a short transaction: a file's rows land together,
// which is what makes an interrupted run resumable.
func (ix *Indexer) write(ctx context.Context, snap, prev int64, parsed []parsedFile, copies []string) error {
	tx, err := ix.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, path := range copies {
		for _, q := range []string{
			`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count)
			 SELECT ?, path, language, content_hash, size, line_count FROM source_files WHERE snapshot_id = ? AND path = ?`,
			`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature)
			 SELECT ?, path, name, qualified_name, kind, start_line, end_line, signature FROM symbols WHERE snapshot_id = ? AND path = ?`,
			`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash)
			 SELECT ?, path, symbol_name, start_line, end_line, content, content_hash FROM chunks WHERE snapshot_id = ? AND path = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, snap, prev, path); err != nil {
				return err
			}
		}
	}
	for _, p := range parsed {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO file_contents(hash, content) VALUES (?, ?)`, p.sha, p.content); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (?, ?, ?, ?, ?, ?)`,
			snap, p.path, p.lang, p.sha, p.size, p.lines); err != nil {
			return err
		}
		if err := insertSymbols(ctx, tx, snap, p.path, p.symbols, ""); err != nil {
			return err
		}
		for _, c := range p.chunks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				snap, p.path, c.Symbol, c.StartLine, c.EndLine, c.Content, contentHash(p.path, c.Symbol, c.Content)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func insertSymbols(ctx context.Context, tx *sql.Tx, snap int64, path string, syms []analyzer.Symbol, parent string) error {
	for _, s := range syms {
		q := s.QualifiedName(parent)
		if _, err := tx.ExecContext(ctx, `INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			snap, path, s.Name, q, s.Kind, s.StartLine, s.EndLine, s.Signature); err != nil {
			return err
		}
		if err := insertSymbols(ctx, tx, snap, path, s.Children, q); err != nil {
			return err
		}
	}
	return nil
}

// prune keeps the published snapshot and the one before it; older snapshots
// keep their row (conversations point at it) and their wiki, but lose their
// indexed data. A snapshot left half emptied, by a stop midway, is finished
// here too, and so are file contents no snapshot uses any more.
func (ix *Indexer) prune(ctx context.Context, repoID, current int64) error {
	rows, err := ix.DB.QueryContext(ctx, `
		SELECT id FROM snapshots s WHERE repository_id = ?1
			AND (status <> 'pruned' OR EXISTS (SELECT 1 FROM source_files f WHERE f.snapshot_id = s.id))
			AND id NOT IN (
				SELECT id FROM snapshots WHERE repository_id = ?1 AND status = 'ready'
				ORDER BY id = ?2 DESC, indexed_at DESC, id DESC LIMIT 2)`, repoID, current)
	if err != nil {
		return err
	}
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		stale = append(stale, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range stale {
		if err := ix.dropSnapshotData(ctx, id); err != nil {
			return err
		}
	}
	return ix.deleteInBatches(ctx, `DELETE FROM file_contents WHERE hash IN (
		SELECT hash FROM file_contents WHERE hash NOT IN (SELECT content_hash FROM source_files) LIMIT 1000)`)
}

// dropSnapshotData marks a snapshot pruned, so readers leave it at once,
// then deletes its index rows (vectors follow chunks by cascade); source
// files go last, which is how prune tells a snapshot that still holds data.
func (ix *Indexer) dropSnapshotData(ctx context.Context, id int64) error {
	if _, err := ix.DB.ExecContext(ctx, `UPDATE snapshots SET status = 'pruned' WHERE id = ?`, id); err != nil {
		return err
	}
	for _, table := range []string{"chunks", "symbols", "source_files"} {
		if err := ix.deleteInBatches(ctx, `DELETE FROM `+table+` WHERE id IN (SELECT id FROM `+table+` WHERE snapshot_id = ? LIMIT 1000)`, id); err != nil {
			return err
		}
	}
	return nil
}

// deleteInBatches repeats a DELETE of at most 1000 rows until it deletes
// none, so no single statement holds the database's write lock for long.
func (ix *Indexer) deleteInBatches(ctx context.Context, query string, args ...any) error {
	for {
		res, err := ix.DB.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
	}
}
