// Package retrieval runs hybrid search over a snapshot: FTS5, exact symbol
// matches and vector similarity, fused with Reciprocal Rank Fusion, then the
// code the best hits import.
package retrieval

import (
	"cmp"
	"context"
	"log"
	"slices"

	"deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/cache"
	"deepcode/internal/repo/index/graph"
	"deepcode/internal/repo/index/vectorstore"
)

type Retriever struct {
	Store   *store.Store
	Graph   *graph.Graph // the imports, for the code the best hits are built on
	Embed   *ai.OpenAI   // nil disables vector search
	Vectors *vectorstore.SQLite
	Cache   *cache.Cache
}

type Hit = store.Hit // one chunk of code found

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
	res.Hits, err = r.Store.Chunks(ctx, ids)
	return res, err
}

// near ranks, by the query's words, the chunks of the files that the files of
// the top ranked ids import: the code they are built on, which the query may
// not name.
func (r *Retriever) near(ctx context.Context, snapshotID int64, query string, ranked []int64) ([]int64, error) {
	top, err := r.Store.Chunks(ctx, ranked[:min(nearHits, len(ranked))])
	if err != nil || len(top) == 0 {
		return nil, err
	}
	paths := make([]string, len(top))
	for i, h := range top {
		paths[i] = h.Path
	}
	files, err := r.Graph.Dependencies(ctx, snapshotID, paths, nearFiles)
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
	return r.Store.Readme(ctx, snapshotID)
}
