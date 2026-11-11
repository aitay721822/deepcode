package store

import (
	"context"
	"database/sql"
	"strconv"
)

// File is a parsed file's rows: its content, its row in the snapshot, its
// symbols and its chunks.
type File struct {
	Path, Lang string
	Hash       string // the content's: the blob's SHA
	Content    string
	Imports    *string // nil (NULL) when no grammar reads the language, or the file is generated
	References *string // nil (NULL) likewise; the names the file calls
	Size       int64
	Lines      int
	Generated  bool
	Symbols    []Symbol // in the order they are stored
	Chunks     []Chunk
}

// Symbol is a file's symbol, named under its parents.
type Symbol struct {
	Name, QualifiedName, Kind string
	StartLine, EndLine        int
	Signature                 string
}

// Chunk is a part of a file's content, with the hash of its embedding input
// and the words of its compound identifiers.
type Chunk struct {
	Symbol               string
	StartLine, EndLine   int
	Content, Hash, Terms string
}

// Write commits one batch in a short transaction: the rows of the files
// copies, unchanged since snapshot prev, copied from it, and the rows of the
// files parsed. A file's rows land together, which is what makes an
// interrupted run resumable.
func (s *Store) Write(ctx context.Context, snap, prev int64, files []File, copies []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
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
	for _, f := range files {
		var imports, references any // NULL
		if f.Imports != nil {
			imports = *f.Imports
		}
		if f.References != nil {
			references = *f.References
		}
		// This version's imports and calls replace what an older one stored (NULL, or read otherwise).
		if _, err := tx.ExecContext(ctx, `INSERT INTO file_contents(hash, content, imports, calls) VALUES (?, ?, ?, ?)
			ON CONFLICT(hash) DO UPDATE SET imports = coalesce(excluded.imports, file_contents.imports),
			calls = coalesce(excluded.calls, file_contents.calls)`, f.Hash, f.Content, imports, references); err != nil {
			return err
		}
		last, err := replaced(ctx, tx, snap, f.Path)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count, generated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			snap, f.Path, f.Lang, f.Hash, f.Size, f.Lines, f.Generated); err != nil {
			return err
		}
		for _, sym := range f.Symbols {
			if _, err := tx.ExecContext(ctx, `INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				snap, f.Path, sym.Name, sym.QualifiedName, sym.Kind, sym.StartLine, sym.EndLine, sym.Signature); err != nil {
				return err
			}
		}
		for _, c := range f.Chunks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash, terms) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				snap, f.Path, c.Symbol, c.StartLine, c.EndLine, c.Content, c.Hash, c.Terms); err != nil {
				return err
			}
		}
		if last > 0 {
			if err := keepVectors(ctx, tx, snap, f.Path, last); err != nil {
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

// Forget drops the rows of files the snapshot should no longer hold, in one
// transaction; vectors follow chunks.
func (s *Store) Forget(ctx context.Context, snap int64, paths []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range paths {
		for _, table := range []string{"chunks", "symbols", "source_files"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE snapshot_id = ? AND path = ?`, snap, p); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Hashes returns the content hash of each file of snapshot snap by path, if
// analyzer version read it; none for snapshot 0 (none).
func (s *Store) Hashes(ctx context.Context, snap int64, version int) (map[string]string, error) {
	return s.pathMap(ctx, `SELECT f.path, f.content_hash FROM source_files f JOIN snapshots s ON s.id = f.snapshot_id
		WHERE f.snapshot_id = ? AND s.analyzer = `+strconv.Itoa(version), snap)
}

// Paths returns the paths snapshot snap holds, each to "".
func (s *Store) Paths(ctx context.Context, snap int64) (map[string]string, error) {
	return s.pathMap(ctx, `SELECT path, '' FROM source_files WHERE snapshot_id = ?`, snap)
}

// pathMap runs a two-column query (path, value) for one snapshot; snapshot 0
// (none) yields an empty map.
func (s *Store) pathMap(ctx context.Context, query string, snap int64) (map[string]string, error) {
	m := map[string]string{}
	if snap == 0 {
		return m, nil
	}
	rows, err := s.DB.QueryContext(ctx, query, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// Untermed passes chunk the id, path, content and content hash of up to
// limit chunks past id after, in id order, whose terms are NULL: stored by
// an older version.
func (s *Store) Untermed(ctx context.Context, after int64, limit int, chunk func(id int64, path, content, hash string)) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, path, content, content_hash FROM chunks WHERE id > ? AND terms IS NULL ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var path, content, hash string
		if err := rows.Scan(&id, &path, &content, &hash); err != nil {
			return err
		}
		chunk(id, path, content, hash)
	}
	return rows.Err()
}

// Terms is a chunk's terms, with the content hash of what they were worked
// out from.
type Terms struct {
	ID          int64
	Hash, Terms string
}

// SetTerms writes a batch of terms in one transaction, to chunks that still
// hold what they were worked out from: one deleted meanwhile and stored again
// at its id with other content is left. It returns how many it set.
func (s *Store) SetTerms(ctx context.Context, batch []Terms) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	set := 0
	for _, t := range batch {
		res, err := tx.ExecContext(ctx, `UPDATE chunks SET terms = ? WHERE id = ? AND content_hash = ?`, t.Terms, t.ID, t.Hash)
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
