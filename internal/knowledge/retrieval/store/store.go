// Package store reads a snapshot's indexed chunks and symbols from SQLite for
// retrieval: the chunks that match a query's words or name its symbols, and
// the chunks themselves.
package store

import (
	"context"
	"database/sql"

	"deepcode/internal/platform/storage"
)

// Store runs retrieval's SQL.
type Store struct{ DB *sql.DB }

type Hit struct {
	ChunkID   int64  `json:"chunkId"`
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Content   string `json:"content"`
}

// Readme returns the first two chunks of the snapshot's top-level README.
func (s *Store) Readme(ctx context.Context, snapshotID int64) ([]Hit, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+hitColumns+` FROM chunks
		WHERE snapshot_id = ? AND path NOT LIKE '%/%' AND lower(path) LIKE 'readme%' ORDER BY start_line LIMIT 2`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		h, err := scanHit(rows)
		if err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// hitColumns are the chunk columns scanHit reads.
const hitColumns = `id, path, symbol_name, start_line, end_line, content`

func scanHit(rows *sql.Rows) (Hit, error) {
	var h Hit
	err := rows.Scan(&h.ChunkID, &h.Path, &h.Symbol, &h.StartLine, &h.EndLine, &h.Content)
	return h, err
}

// Chunks reads the chunks for ids, keeping the order of ids.
func (s *Store) Chunks(ctx context.Context, ids []int64) ([]Hit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+hitColumns+` FROM chunks WHERE id IN (`+storage.Placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[int64]Hit{}
	for rows.Next() {
		h, err := scanHit(rows)
		if err != nil {
			return nil, err
		}
		byID[h.ChunkID] = h
	}
	hits := make([]Hit, 0, len(ids))
	for _, id := range ids {
		if h, ok := byID[id]; ok {
			hits = append(hits, h)
		}
	}
	return hits, rows.Err()
}

func scanIDs(rows *sql.Rows, err error) ([]int64, error) {
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
