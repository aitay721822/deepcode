// Package retrieval runs hybrid search over a snapshot: FTS5, exact symbol
// matches, vector similarity and the files a query names, fused with
// Reciprocal Rank Fusion, then the code the best hits import.
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
	Named  int   `json:"named"` // chunks of the files the query names (named)
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
	named, firsts, err := r.named(ctx, snapshotID, query)
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
	ids := sure(fuse(fts, vec, sym, named), named[:firsts], head(limit))
	near, err := r.near(ctx, snapshotID, query, ids)
	if err != nil {
		return res, err
	}
	ids, res.Graph = supplement(ids, near, limit)
	res.FTS, res.Vector, res.Symbol, res.Named = len(fts), len(vec), len(sym), len(named)
	if res.Hits, err = r.Store.Chunks(ctx, ids); err != nil {
		return res, err
	}
	res.Hits, err = r.declared(ctx, snapshotID, res.Hits)
	return res, err
}

// declared joins each hit on a file's header, the licence, package and
// imports before its first declaration, to the chunk declaring it: alone the
// header shows no code, yet its imports can win a search for what the code
// does (OwnerRepository.java's JpaRepository), and the declaration may rank
// nowhere. Both found are read as one, in the place of the first.
// ponytail: at search time, as joining them when chunking ranked the eval's
// Java corpora lower; chunk headers apart if a corpus needs it.
func (r *Retriever) declared(ctx context.Context, snapshotID int64, hits []Hit) ([]Hit, error) {
	drop := map[int]bool{}
	for i, h := range hits {
		if h.Symbol != "" || h.StartLine != 1 || drop[i] {
			continue
		}
		next, ok, err := r.Store.Following(ctx, snapshotID, h.Path, h.EndLine+1)
		if err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		at := i
		if j := slices.IndexFunc(hits, func(o Hit) bool { return o.ChunkID == next.ChunkID }); j >= 0 {
			at, drop[max(i, j)] = min(i, j), true
		}
		hits[at] = Hit{ChunkID: h.ChunkID, Path: h.Path, Symbol: next.Symbol, StartLine: h.StartLine, EndLine: next.EndLine, Content: h.Content + "\n" + next.Content}
	}
	out := hits[:0]
	for i, h := range hits {
		if !drop[i] {
			out = append(out, h)
		}
	}
	return out, nil
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
	head := min(len(ranked), head(limit))
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

// head is how many of a search's limit places the ranked chunks hold before
// the code they import may take the rest (supplement).
func head(limit int) int { return max(limit-nearSlots, 1) }

// sure is ids with each of must among the first n: one ranked past them takes
// the last of those places not held by another of must, the ids it displaces
// moving down. must are the best chunks of the files a query names, which
// fusion ranks below any chunk two searches find: eShop's AppHost.cs, a long
// file few of its page's words match, or an example placed in a page about
// another, whose words it shares with none.
func sure(ids, must []int64, n int) []int64 {
	n = min(n, len(ids))
	is := map[int64]bool{}
	for _, id := range must {
		is[id] = true
	}
	var late []int64
	for _, id := range ids[n:] {
		if is[id] {
			late = append(late, id)
		}
	}
	if len(late) == 0 {
		return ids
	}
	head := slices.Clone(ids[:n])
	room := len(late)
	for i := len(head) - 1; i >= 0 && room > 0; i-- {
		if !is[head[i]] {
			head, room = slices.Delete(head, i, i+1), room-1
		}
	}
	out := append(head, late[:len(late)-room]...)
	placed := map[int64]bool{}
	for _, id := range out {
		placed[id] = true
	}
	for _, id := range ids {
		if !placed[id] {
			out = append(out, id)
		}
	}
	return out
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
