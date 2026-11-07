package store

import (
	"context"
	"database/sql"

	"deepcode/internal/platform/storage"
)

// ChunkIDs returns the lowest and highest id of the snapshot's chunks; ok is
// false when it has none.
func (s *Store) ChunkIDs(ctx context.Context, snap int64) (lo, hi int64, ok bool, err error) {
	var l, h sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT min(id), max(id) FROM chunks WHERE snapshot_id = ?`, snap).Scan(&l, &h); err != nil {
		return 0, 0, false, err
	}
	return l.Int64, h.Int64, l.Valid, nil
}

// Reuse copies the vectors of model stored for identical content, from any
// snapshot, onto the snapshot's chunks with ids from up to before to.
func (s *Store) Reuse(ctx context.Context, snap int64, model string, from, to int64) error {
	// Each content in the range looks its stored vector up once, not each
	// chunk: a file can repeat one chunk thousands of times, and pairing
	// every copy with every other took a minute for one such file
	// (MATERIALIZED keeps SQLite from folding the lookup back into the
	// join). The + keeps SQLite on the id range: through chunks_path it
	// would scan all of the snapshot's chunks for every range.
	_, err := s.DB.ExecContext(ctx, `
		WITH stored AS MATERIALIZED (
			SELECT h.content_hash, (SELECT e.chunk_id FROM chunks o JOIN chunk_embeddings e ON e.chunk_id = o.id AND e.model = ?1
				WHERE o.content_hash = h.content_hash LIMIT 1) chunk_id
			FROM (SELECT DISTINCT content_hash FROM chunks WHERE +snapshot_id = ?2 AND id >= ?3 AND id < ?4) h)
		INSERT OR IGNORE INTO chunk_embeddings(chunk_id, model, dimension, embedding)
		SELECT c.id, e.model, e.dimension, e.embedding FROM stored s
		JOIN chunks c ON c.content_hash = s.content_hash
		JOIN chunk_embeddings e ON e.chunk_id = s.chunk_id AND e.model = ?1
		WHERE +c.snapshot_id = ?2 AND c.id >= ?3 AND c.id < ?4`, model, snap, from, to)
	return err
}

// Pending returns one chunk id per distinct content of the snapshot that
// still lacks a vector of model.
func (s *Store) Pending(ctx context.Context, snap int64, model string) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT min(c.id) FROM chunks c
		WHERE c.snapshot_id = ? AND NOT EXISTS (SELECT 1 FROM chunk_embeddings e WHERE e.chunk_id = c.id AND e.model = ?)
		GROUP BY c.content_hash ORDER BY 1`, snap, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Input is what a chunk's embedding input is made of, with its content hash.
type Input struct{ Path, Symbol, Content, Hash string }

// Inputs returns what the embedding inputs of chunks ids are made of.
func (s *Store) Inputs(ctx context.Context, ids []int64) ([]Input, error) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT path, symbol_name, content, content_hash FROM chunks WHERE id IN (`+storage.Placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Input
	for rows.Next() {
		var in Input
		if err := rows.Scan(&in.Path, &in.Symbol, &in.Content, &in.Hash); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// WithHashes returns the content hash of each of the snapshot's chunks whose
// content hash is one of hashes, by chunk id.
func (s *Store) WithHashes(ctx context.Context, snap int64, hashes []string) (map[int64]string, error) {
	args := []any{snap}
	for _, h := range hashes {
		args = append(args, h)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, content_hash FROM chunks WHERE snapshot_id = ? AND content_hash IN (`+storage.Placeholders(len(hashes))+`)`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, err
		}
		out[id] = hash
	}
	return out, rows.Err()
}
