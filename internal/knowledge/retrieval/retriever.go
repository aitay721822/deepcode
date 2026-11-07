// Package retrieval runs hybrid search over a snapshot: FTS5, exact symbol
// matches and vector similarity, fused with Reciprocal Rank Fusion, then the
// code the best hits import.
package retrieval

import (
	"cmp"
	"context"
	"database/sql"
	"log"
	"slices"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/graph"
	"deepcode/internal/repo/index/vectorstore"
)

type Retriever struct {
	DB      *sql.DB
	Embed   *ai.OpenAI // nil disables vector search
	Vectors *vectorstore.SQLite
	Cache   *cache.Cache
}

type Hit struct {
	ChunkID   int64  `json:"chunkId"`
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Content   string `json:"content"`
}

type Result struct {
	Hits   []Hit `json:"hits"`
	FTS    int   `json:"fts"`
	Vector int   `json:"vector"`
	Symbol int   `json:"symbol"`
	Graph  int   `json:"graph"`
}

const (
	ftsLimit    = 25
	vectorLimit = 25
	symbolLimit = 10
	rrfK        = 60

	// The code the best hits import: the files the top nearHits chunks' files
	// import, at most nearFiles, searched for at most nearLimit chunks, of
	// which up to nearSlots take the last places of a search's limit.
	nearHits  = 5
	nearFiles = 40
	nearLimit = 10
	nearSlots = 2
)

// Search returns the top chunks of a snapshot for a natural-language or
// identifier query. A failing embedding provider degrades the search to FTS5
// and symbols instead of failing it.
func (r *Retriever) Search(ctx context.Context, snapshotID int64, query string, limit int) (Result, error) {
	var res Result
	fts, err := r.fts(ctx, snapshotID, query, nil, ftsLimit)
	if err != nil {
		return res, err
	}
	sym, err := r.symbolChunks(ctx, snapshotID, query)
	if err != nil {
		return res, err
	}
	var vec []int64
	if r.Embed != nil {
		if vec, err = r.vector(ctx, snapshotID, query); err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			log.Printf("retrieval: vector search unavailable, using FTS5 and symbols only: %v", err)
		}
	}
	ids := fuse(fts, vec, sym)
	near, err := r.near(ctx, snapshotID, query, ids)
	if err != nil {
		return res, err
	}
	ids, res.Graph = supplement(ids, near, limit)
	res.FTS, res.Vector, res.Symbol = len(fts), len(vec), len(sym)
	res.Hits, err = r.load(ctx, ids)
	return res, err
}

// near ranks, by the query's words, the chunks of the files that the files of
// the top ranked ids import: the code they are built on, which the query may
// not name.
func (r *Retriever) near(ctx context.Context, snapshotID int64, query string, ranked []int64) ([]int64, error) {
	top, err := r.load(ctx, ranked[:min(nearHits, len(ranked))])
	if err != nil || len(top) == 0 {
		return nil, err
	}
	paths := make([]string, len(top))
	for i, h := range top {
		paths[i] = h.Path
	}
	files, err := graph.Dependencies(ctx, r.DB, snapshotID, paths, nearFiles)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	return r.fts(ctx, snapshotID, query, files, nearLimit)
}

// supplement is the first limit of ranked, except that up to nearSlots of
// the last places, the first place aside, go to chunks of near that the
// places before lack: the imported code is read beside the best hits, never
// instead of them. A fused list would put any hit found both ways first,
// however weak. It also returns how many places near took.
func supplement(ranked, near []int64, limit int) ([]int64, int) {
	head := min(len(ranked), max(limit-nearSlots, 1))
	out := slices.Clone(ranked[:head])
	seen := map[int64]bool{}
	for _, id := range out {
		seen[id] = true
	}
	add := func(ids []int64, upTo int) {
		for _, id := range ids {
			if len(out) >= upTo {
				return
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(near, min(limit, head+nearSlots))
	took := len(out) - head
	add(ranked[head:], limit)
	return out, took
}

// fuse ranks ids by Reciprocal Rank Fusion: score = Σ 1/(k + rank).
func fuse(lists ...[]int64) []int64 {
	score := map[int64]float64{}
	for _, l := range lists {
		for rank, id := range l {
			score[id] += 1 / float64(rrfK+rank+1)
		}
	}
	ids := make([]int64, 0, len(score))
	for id := range score {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int64) int {
		return cmp.Or(cmp.Compare(score[b], score[a]), cmp.Compare(a, b))
	})
	return ids
}

// Readme returns the first two chunks of the snapshot's top-level README,
// which the wiki's overview starts from.
func (r *Retriever) Readme(ctx context.Context, snapshotID int64) ([]Hit, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT `+hitColumns+` FROM chunks
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

// load reads the chunks for ids, keeping the order of ids.
func (r *Retriever) load(ctx context.Context, ids []int64) ([]Hit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT `+hitColumns+` FROM chunks WHERE id IN (`+storage.Placeholders(len(ids))+`)`, args...)
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
