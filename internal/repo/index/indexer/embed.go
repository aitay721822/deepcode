package indexer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"

	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
)

const (
	embedBatchSize = 32
	reuseBatch     = 1000 // chunk ids per copy of stored vectors, so no copy holds the write lock for long
)

// embedInput is the exact text embedded for a chunk; the path and symbol give
// the model context the code alone lacks.
func embedInput(path, symbol, content string) string {
	s := "File: " + path + "\n"
	if symbol != "" {
		s += "Symbol: " + symbol + "\n"
	}
	s += "\n" + content
	if len(s) > 8000 {
		s = strings.ToValidUTF8(s[:8000], "")
	}
	return s
}

// contentHash covers the exact embedding input, so equal hashes can share a vector.
func contentHash(path, symbol, content string) string {
	sum := sha256.Sum256([]byte(embedInput(path, symbol, content)))
	return hex.EncodeToString(sum[:])
}

// embed fills in vectors for the snapshot's chunks: first by copying vectors of
// identical content from any snapshot, then by embedding each remaining
// distinct content once.
func (ix *Indexer) embed(ctx context.Context, snap int64, progress jobs.Progress) error {
	model := ix.Embed.ModelName
	progress.Stage("embed", 0, 0)
	if err := ix.reuse(ctx, snap, model); err != nil {
		return err
	}
	pending, err := ix.pending(ctx, snap, model)
	if err != nil {
		return err
	}
	progress.Logf("embedding %d distinct chunks with %s; the rest reuse stored vectors", len(pending), model)
	for i := 0; i < len(pending); i += embedBatchSize {
		ids := pending[i:min(i+embedBatchSize, len(pending))]
		if err := ix.embedBatch(ctx, snap, model, ids); err != nil {
			return err
		}
		progress.Stage("embed", i+len(ids), len(pending))
	}
	return nil
}

// reuse copies vectors stored for identical content, from any snapshot, onto
// the snapshot's chunks, one range of reuseBatch chunk ids at a time.
func (ix *Indexer) reuse(ctx context.Context, snap int64, model string) error {
	var lo, hi sql.NullInt64
	if err := ix.DB.QueryRowContext(ctx, `SELECT min(id), max(id) FROM chunks WHERE snapshot_id = ?`, snap).Scan(&lo, &hi); err != nil {
		return err
	}
	for from := lo.Int64; lo.Valid && from <= hi.Int64; from += reuseBatch {
		// Each content in the range looks its stored vector up once, not each
		// chunk: a file can repeat one chunk thousands of times, and pairing
		// every copy with every other took a minute for one such file
		// (MATERIALIZED keeps SQLite from folding the lookup back into the
		// join). The + keeps SQLite on the id range: through chunks_path it
		// would scan all of the snapshot's chunks for every range.
		if _, err := ix.DB.ExecContext(ctx, `
			WITH stored AS MATERIALIZED (
				SELECT h.content_hash, (SELECT e.chunk_id FROM chunks o JOIN chunk_embeddings e ON e.chunk_id = o.id AND e.model = ?1
					WHERE o.content_hash = h.content_hash LIMIT 1) chunk_id
				FROM (SELECT DISTINCT content_hash FROM chunks WHERE +snapshot_id = ?2 AND id >= ?3 AND id < ?4) h)
			INSERT OR IGNORE INTO chunk_embeddings(chunk_id, model, dimension, embedding)
			SELECT c.id, e.model, e.dimension, e.embedding FROM stored s
			JOIN chunks c ON c.content_hash = s.content_hash
			JOIN chunk_embeddings e ON e.chunk_id = s.chunk_id AND e.model = ?1
			WHERE +c.snapshot_id = ?2 AND c.id >= ?3 AND c.id < ?4`, model, snap, from, from+reuseBatch); err != nil {
			return err
		}
	}
	return nil
}

// pending returns one chunk id per distinct content that still lacks a vector.
func (ix *Indexer) pending(ctx context.Context, snap int64, model string) ([]any, error) {
	rows, err := ix.DB.QueryContext(ctx, `
		SELECT min(c.id) FROM chunks c
		WHERE c.snapshot_id = ? AND NOT EXISTS (SELECT 1 FROM chunk_embeddings e WHERE e.chunk_id = c.id AND e.model = ?)
		GROUP BY c.content_hash ORDER BY 1`, snap, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []any
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// embedBatch embeds the given chunks and stores each vector on every chunk of
// the snapshot with the same content.
func (ix *Indexer) embedBatch(ctx context.Context, snap int64, model string, ids []any) error {
	rows, err := ix.DB.QueryContext(ctx, `SELECT path, symbol_name, content, content_hash FROM chunks WHERE id IN (`+storage.Placeholders(len(ids))+`)`, ids...)
	if err != nil {
		return err
	}
	var texts []string
	var hashes []any
	for rows.Next() {
		var path, symbol, content, hash string
		if err := rows.Scan(&path, &symbol, &content, &hash); err != nil {
			rows.Close()
			return err
		}
		texts = append(texts, embedInput(path, symbol, content))
		hashes = append(hashes, hash)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	vecs, err := ix.Embed.Embed(ctx, texts)
	if err != nil {
		return err
	}
	byHash := map[string][]float32{}
	for j, h := range hashes {
		byHash[h.(string)] = vecs[j]
	}
	rows, err = ix.DB.QueryContext(ctx, `SELECT id, content_hash FROM chunks WHERE snapshot_id = ? AND content_hash IN (`+storage.Placeholders(len(hashes))+`)`,
		append([]any{snap}, hashes...)...)
	if err != nil {
		return err
	}
	vectors := map[int64][]float32{}
	for rows.Next() {
		var id int64
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			return err
		}
		vectors[id] = byHash[hash]
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return ix.Vectors.Upsert(ctx, model, vectors)
}
