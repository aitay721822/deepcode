// Package retrieval runs hybrid search over a snapshot: FTS5, exact symbol
// matches, vector similarity and the files a query names, fused with
// Reciprocal Rank Fusion, then the code the best hits import.
package retrieval

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"

	"deepcode/internal/knowledge/retrieval/fusion"
	"deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/compat"
	"deepcode/internal/platform/cache"
	"deepcode/internal/repo/index/graph"
	"deepcode/internal/repo/index/indexer/vectorstore"
)

type Retriever struct {
	Store   *store.Store
	Graph   *graph.Graph   // the imports, for the code the best hits are built on
	Embed   *compat.Client // nil disables vector search
	Vectors *vectorstore.SQLite
	Cache   *cache.Cache
}

type Hit = store.Hit // one chunk of code found

// Anchor is a line of a file a search is sure to read: the chunk holding it,
// or the file's first for Line 0.
type Anchor struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

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

	// The code the best hits import: the files the top nearHits chunks' files
	// import, at most nearFiles, searched for at most nearLimit chunks, of
	// which up to nearSlots take the last places of a search's limit.
	nearHits  = 5
	nearFiles = 40
	nearLimit = 10
	nearSlots = 2
)

// Search returns the top chunks of a snapshot for a natural-language or
// identifier query, one hit a file, the first of it the ranking reaches, so
// that one file's many chunks leave room for the files ranked below it. A failing embedding
// provider degrades the search to FTS5 and symbols instead of failing it.
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
	ranked := fusion.Sure(fusion.Fuse(fts, vec, sym, named), named[:firsts], head(limit))
	near, err := r.near(ctx, snapshotID, query, ranked)
	if err != nil {
		return res, err
	}
	ids, graph := supplement(ranked, near, limit)
	res.FTS, res.Vector, res.Symbol, res.Named, res.Graph = len(fts), len(vec), len(sym), len(named), graph
	res.Hits, err = r.Store.Read(ctx, snapshotID, then(ids, ranked), limit, true)
	return res, err
}

// SearchPage is a wiki page's evidence: first the chunk holding each of
// anchors, in their order, sure of a place while limit allows; then the code
// those anchors reach (Store.Reached: the rest of a declaration, its members,
// the functions of its file it names), each anchor's next in turn, outside
// limit, as a heading promises its declaration whole; then FTS5 on words
// within files and across the snapshot, symbols and vector search, fused as
// Search fuses them, and the code the best hits import, in what limit leaves
// the anchors. The caller's budget cuts the list where it stops fitting, so
// its order is the priority. It reads files as given, never parsing names
// from words, so a path holding a space is read whole. An anchor whose chunk
// is taken costs nothing more; one past limit, or whose line no chunk holds,
// is left out with what it reaches, which the page's skeleton sees in what
// its evidence shows. Unlike Search, a file may take several places, as a
// page draws on a file's many parts and its anchors may share a file. An
// embedding server it cannot reach fails it (ai.ErrUnavailable), where
// Search goes on without vectors: a page is stored, so it waits for the
// evidence a later try finds.
func (r *Retriever) SearchPage(ctx context.Context, snapshotID int64, words string, files []string, anchors []Anchor, limit int) (Result, error) {
	var res Result
	fts, err := r.fts(ctx, snapshotID, words, nil, ftsLimit)
	if err != nil {
		return res, err
	}
	sym, err := r.symbolChunks(ctx, snapshotID, words)
	if err != nil {
		return res, err
	}
	var own []int64
	if len(files) > 0 {
		if own, err = r.fts(ctx, snapshotID, words, files, ftsLimit); err != nil {
			return res, err
		}
	}
	var vec []int64
	if r.Embed != nil {
		if vec, err = r.vector(ctx, snapshotID, words); err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			if errors.Is(err, ai.ErrUnavailable) { // a page is kept: written once the server is back, not from less evidence now
				return res, err
			}
			log.Printf("retrieval: vector search unavailable, using FTS5 and symbols only: %v", err)
		}
	}
	paths, lines := make([]string, len(anchors)), make([]int, len(anchors))
	for i, a := range anchors {
		paths[i], lines[i] = a.Path, a.Line
	}
	sure, err := r.Store.Holding(ctx, snapshotID, paths, lines)
	if err != nil {
		return res, err
	}
	var first []int64
	for i, id := range sure {
		if id == 0 || !slices.Contains(first, id) && len(first) >= limit {
			lines[i] = 0 // left out: it reaches nothing (Reached)
		} else if !slices.Contains(first, id) {
			first = append(first, id)
		}
	}
	lists, err := r.Store.Reached(ctx, snapshotID, paths, lines)
	if err != nil {
		return res, err
	}
	var reached []int64
	for i, more := 0, true; more; i++ { // each list's i-th in turn, a list run out skipped
		more = false
		for _, l := range lists {
			if i < len(l) {
				more = true
				if !slices.Contains(first, l[i]) && !slices.Contains(reached, l[i]) {
					reached = append(reached, l[i])
				}
			}
		}
	}
	taken := slices.Concat(first, reached)
	isTaken := func(id int64) bool { return slices.Contains(taken, id) }
	fused := slices.DeleteFunc(fusion.Fuse(fts, vec, sym, own), isTaken)
	ids := taken
	if room := limit - len(first); room > 0 && len(fused) > 0 {
		near, err := r.near(ctx, snapshotID, words, fused)
		if err != nil {
			return res, err
		}
		var rest []int64
		rest, res.Graph = supplement(fused, slices.DeleteFunc(near, isTaken), room)
		ids = append(ids, rest...)
	}
	res.FTS, res.Vector, res.Symbol, res.Named = len(fts), len(vec), len(sym), len(own)
	res.Hits, err = r.Store.Read(ctx, snapshotID, then(ids, fused), limit+len(reached), false)
	return res, err
}

// then is ids, then the ranked ids they lack, in rank order: the chunks read
// in the places a header joined to its declaration frees (Store.Read).
func then(ids, ranked []int64) []int64 {
	return append(slices.Clip(ids), slices.DeleteFunc(slices.Clone(ranked), func(id int64) bool { return slices.Contains(ids, id) })...)
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

// Readme returns the first two chunks of the snapshot's top-level README,
// which the wiki's overview starts from.
func (r *Retriever) Readme(ctx context.Context, snapshotID int64) ([]Hit, error) {
	return r.Store.Readme(ctx, snapshotID)
}

type SymbolHit = store.SymbolHit // a symbol the search behind the UI found

// Symbols is the symbol search behind the UI: substring match on names,
// exact matches first, then prefixes, then shorter names.
func (r *Retriever) Symbols(ctx context.Context, snapshotID int64, q string, limit int) ([]SymbolHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []SymbolHit{}, nil
	}
	return r.Store.Symbols(ctx, snapshotID, q, limit)
}

// Users returns chunks of the files that import a file defining one of names
// and use that name: the code that calls, handles or builds it, which a search
// for the name finds only when its words win. Each file gives one chunk, the
// files using most of names first, tests, docs and generated files aside; at
// most limit. Names match exactly, case and all, as the code holds them.
func (r *Retriever) Users(ctx context.Context, snapshotID int64, names []string, limit int) ([]Hit, error) {
	if len(names) == 0 {
		return nil, nil
	}
	ids, err := r.Store.Users(ctx, snapshotID, names, limit)
	if err != nil {
		return nil, err
	}
	return r.Store.Chunks(ctx, ids)
}

func (r *Retriever) SCIPUsers(ctx context.Context, snapshotID int64, name string, at Anchor, limit int) ([]store.SCIPHit, bool, error) {
	return r.Store.SCIPUsers(ctx, snapshotID, name, at.Path, at.Line, limit)
}
