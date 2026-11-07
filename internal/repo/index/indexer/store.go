// The snapshot's rows in SQLite: batches committed while parsing, the terms
// an older version left out filled in, and the data of older snapshots pruned
// once a newer one is published.

package indexer

import (
	"context"
	"database/sql"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/chunker"
	"deepcode/internal/repo/index/graph"
)

// termsBatch is how many chunks FillTerms splits in one transaction.
const termsBatch = 500

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
			`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count, generated)
			 SELECT ?, path, language, content_hash, size, line_count, generated FROM source_files WHERE snapshot_id = ? AND path = ?`,
			`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature)
			 SELECT ?, path, name, qualified_name, kind, start_line, end_line, signature FROM symbols WHERE snapshot_id = ? AND path = ?`,
			`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash, terms)
			 SELECT ?, path, symbol_name, start_line, end_line, content, content_hash, terms FROM chunks WHERE snapshot_id = ? AND path = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, snap, prev, path); err != nil {
				return err
			}
		}
	}
	for _, p := range parsed {
		var imports any                                  // NULL: no grammar reads the language, or the file is generated
		if analyzer.HasGrammar(p.lang) && !p.generated { // a hand-written copy fills them in
			imports = graph.Imports(p.imports)
		}
		// This version's imports replace what an older one stored (NULL, or read otherwise).
		if _, err := tx.ExecContext(ctx, `INSERT INTO file_contents(hash, content, imports) VALUES (?, ?, ?)
			ON CONFLICT(hash) DO UPDATE SET imports = coalesce(excluded.imports, file_contents.imports)`, p.sha, p.content, imports); err != nil {
			return err
		}
		last, err := replaced(ctx, tx, snap, p.path)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count, generated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			snap, p.path, p.lang, p.sha, p.size, p.lines, p.generated); err != nil {
			return err
		}
		if err := insertSymbols(ctx, tx, snap, p.path, p.symbols, ""); err != nil {
			return err
		}
		for _, c := range p.chunks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash, terms) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				snap, p.path, c.Symbol, c.StartLine, c.EndLine, c.Content, contentHash(p.path, c.Symbol, c.Content), chunker.Terms(p.path, c.Content)); err != nil {
				return err
			}
		}
		if last > 0 {
			if err := keepVectors(ctx, tx, snap, p.path, last); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// replaced readies a file the snapshot holds already, read again in place
// after an analyzer upgrade, to be stored anew: its symbols and file row go,
// and last is its newest chunk's id (0 for none), whose chunks keepVectors
// drops once the new ones are in.
func replaced(ctx context.Context, tx *sql.Tx, snap int64, path string) (last int64, err error) {
	var newest sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(id) FROM chunks WHERE snapshot_id = ? AND path = ?`, snap, path).Scan(&newest); err != nil {
		return 0, err
	}
	for _, table := range []string{"symbols", "source_files"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE snapshot_id = ? AND path = ?`, snap, path); err != nil {
			return 0, err
		}
	}
	return newest.Int64, nil
}

// keepVectors gives the file's new chunks the vectors of its old ones (ids up
// to last) with the same content, then drops the old ones: an upgrade embeds
// only what it changed, and search keeps them meanwhile.
func keepVectors(ctx context.Context, tx *sql.Tx, snap int64, path string, last int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chunk_embeddings(chunk_id, model, dimension, embedding)
		SELECT n.id, e.model, e.dimension, e.embedding FROM chunks n
		JOIN chunks o ON o.snapshot_id = n.snapshot_id AND o.path = n.path AND o.id <= ?3 AND o.content_hash = n.content_hash
		JOIN chunk_embeddings e ON e.chunk_id = o.id
		WHERE n.snapshot_id = ?1 AND n.path = ?2 AND n.id > ?3`, snap, path, last); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE snapshot_id = ? AND path = ? AND id <= ?`, snap, path, last)
	return err
}

// forget drops the rows of files the snapshot should no longer hold, in one
// transaction.
func (ix *Indexer) forget(ctx context.Context, snap int64, paths []string) error {
	tx, err := ix.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range paths {
		if err := dropFile(ctx, tx, snap, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// dropFile deletes a file's rows from the snapshot; vectors follow chunks.
func dropFile(ctx context.Context, tx *sql.Tx, snap int64, path string) error {
	for _, table := range []string{"chunks", "symbols", "source_files"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE snapshot_id = ? AND path = ?`, snap, path); err != nil {
			return err
		}
	}
	return nil
}

// FillTerms gives the chunks an older version stored, whose terms are NULL,
// the words of their compound identifiers, termsBatch at a time in id order
// so no transaction holds the write lock for long. Until then such a chunk is
// found by its content alone, as before; a stop leaves the rest to the next
// start. It returns how many chunks it filled.
func (ix *Indexer) FillTerms(ctx context.Context) (int, error) {
	filled := 0
	for last := int64(0); ; {
		rows, err := ix.DB.QueryContext(ctx, `SELECT id, path, content, content_hash FROM chunks WHERE id > ? AND terms IS NULL ORDER BY id LIMIT ?`, last, termsBatch)
		if err != nil {
			return filled, err
		}
		var batch []filling
		for rows.Next() {
			var f filling
			var path, content string
			if err := rows.Scan(&f.id, &path, &content, &f.hash); err != nil {
				rows.Close()
				return filled, err
			}
			f.terms = chunker.Terms(path, content)
			batch = append(batch, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(batch) == 0 {
			return filled, err
		}
		set, err := ix.setTerms(ctx, batch)
		if err != nil {
			return filled, err
		}
		filled, last = filled+set, batch[len(batch)-1].id
	}
}

// filling is a chunk's terms as FillTerms worked them out, with the content
// hash of what it read.
type filling struct {
	id          int64
	hash, terms string
}

// setTerms writes a batch of FillTerms' work in one transaction, to chunks
// that still hold what it read: one deleted meanwhile and stored again at its
// id with other content is left for the next pass. It returns how many it set.
func (ix *Indexer) setTerms(ctx context.Context, batch []filling) (int, error) {
	tx, err := ix.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	set := 0
	for _, f := range batch {
		res, err := tx.ExecContext(ctx, `UPDATE chunks SET terms = ? WHERE id = ? AND content_hash = ?`, f.terms, f.id, f.hash)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		set += int(n)
	}
	return set, tx.Commit()
}

// insertSymbols stores the file's symbols and their children's, named under parent.
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
	if err := graph.Unlink(ctx, ix.DB, id); err != nil {
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
