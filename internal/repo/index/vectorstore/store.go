// Package vectorstore keeps chunk embeddings in SQLite and ranks them against
// a query vector.
package vectorstore

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
)

type Hit struct {
	ChunkID int64
	Score   float32
}

// SQLite keeps normalised float32 vectors as BLOBs and ranks them by cosine
// similarity (a dot product, since both sides are normalised). Vectors are
// deleted with their chunk by a foreign-key cascade.
// ponytail: loads the snapshot's vectors on every search, O(N×d); fine for the
// ~50k LOC target, cache hot snapshots or move to an ANN index past that.
type SQLite struct {
	DB *sql.DB
}

func (s *SQLite) Upsert(ctx context.Context, model string, vectors map[int64][]float32) error {
	if len(vectors) == 0 {
		return nil
	}
	dim := -1
	if err := s.DB.QueryRowContext(ctx, `SELECT dimension FROM chunk_embeddings WHERE model = ? LIMIT 1`, model).Scan(&dim); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, v := range vectors {
		if dim == -1 {
			dim = len(v)
		}
		if len(v) != dim || dim == 0 {
			return fmt.Errorf("embedding model %s returned %d dimensions, expected %d", model, len(v), dim)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO chunk_embeddings(chunk_id, model, dimension, embedding) VALUES (?, ?, ?, ?)`,
			id, model, dim, Encode(normalize(v))); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) Search(ctx context.Context, snapshotID int64, model string, query []float32, limit int) ([]Hit, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT e.chunk_id, e.embedding FROM chunk_embeddings e JOIN chunks c ON c.id = e.chunk_id
		WHERE c.snapshot_id = ? AND e.model = ?`, snapshotID, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	q := normalize(query)
	var hits []Hit
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		if len(blob) != 4*len(q) {
			return nil, fmt.Errorf("query has %d dimensions but stored vectors have %d", len(q), len(blob)/4)
		}
		var dot float32
		for i := range q {
			dot += q[i] * math.Float32frombits(binary.LittleEndian.Uint32(blob[4*i:]))
		}
		hits = append(hits, Hit{ChunkID: id, Score: dot})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.ChunkID, b.ChunkID))
	})
	return hits[:min(limit, len(hits))], nil
}
