// Package eval measures retrieval on questions whose answers are known: each
// repository is indexed at a pinned commit, and each question is searched the
// way chat searches it. eval_test.go holds recall@10 and MRR at or above the
// floors recorded with the questions (cases.go); the bench (test/bench)
// reports them beside its other numbers.
//
// deepcode's own questions run with every go test. The other repositories'
// need their history: set DEEPCODE_EVAL_GIT to a folder holding a bare clone
// of each, named after its corpus (git clone --bare <url> chi.git). With
// DEEPCODE_EMBED_MODEL set, chunks are embedded and searched as deepcode
// serve does (Embedder) and, for HybridModel, held to the hybrid floors
// (another model's scores are logged).
package eval

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"deepcode/cmd/deepcode/config"
	"deepcode/internal/app"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
)

// Embedder is the embedding model deepcode serve searches with, read as serve
// reads it (DEEPCODE_EMBED_MODEL; DEEPCODE_EMBED_BASE_URL and _API_KEY, else
// the chat's), or nil without one, which leaves vector search off.
func Embedder() (*ai.OpenAI, error) {
	cfg, _, err := config.Load()
	if err != nil {
		return nil, err
	}
	_, embed := app.Providers(cfg)
	return embed, nil
}

// Hits is how many results a question reads: as many as chat's evidence.
const Hits = 10

type Question struct {
	Ask   string   // as a user asks it
	Terms string   // for a question none of whose words the code holds: the model's search terms for it, frozen
	Want  []string // the files that answer it; file#Name wants the chunk of file that holds symbol Name
}

type Corpus struct {
	Name, URL, SHA          string  // URL: where the clone comes from; "" for this repository
	Recall, MRR             float64 // the floors of the lexical search
	HybridRecall, HybridMRR float64 // the floors with HybridModel's embeddings
	Questions               []Question
}

// HybridModel is the embedding model the hybrid floors were measured with
// (served by LM Studio); another's scores are logged, not held to them. The
// floors are exact for that setup: the same name quantised or served another
// way can move a rank, so re-measure the old code before blaming a change.
const HybridModel = "text-embedding-qwen3-embedding-0.6b"

// Result is a corpus's scores, with a line for each question that missed a
// file it wants.
type Result struct {
	Recall, MRR float64
	Misses      []string
}

// Run asks c's questions of the snapshot snap that r searches.
func Run(ctx context.Context, r *retrieval.Retriever, snap int64, c Corpus) (Result, error) {
	var res Result
	for _, q := range c.Questions {
		hits, err := search(ctx, r, snap, q)
		if err != nil {
			return res, err
		}
		rec, rr := score(hits, q.Want)
		res.Recall, res.MRR = res.Recall+rec, res.MRR+rr
		if rec < 1 {
			files := make([]string, len(hits))
			for i, h := range hits {
				files[i] = h.Path
			}
			res.Misses = append(res.Misses, fmt.Sprintf("%q: want %v, found %v", q.Ask, q.Want, files))
		}
	}
	n := float64(max(len(c.Questions), 1))
	res.Recall, res.MRR = res.Recall/n, res.MRR/n
	return res, nil
}

// search returns q's hits in order, searched as chat's search
// does it: the question alone, then, when none of its words match the code,
// with the model's terms in front.
// ponytail: a copy of chat.Service.search's rule, without its model; follow
// it if that rule changes.
func search(ctx context.Context, r *retrieval.Retriever, snap int64, q Question) ([]retrieval.Hit, error) {
	res, err := r.Search(ctx, snap, q.Ask, Hits)
	if err == nil && res.FTS == 0 {
		if q.Terms == "" {
			return nil, fmt.Errorf("%q matches no word of the code: freeze the model's search terms for it", q.Ask)
		}
		terms := strings.Fields(q.Terms)
		res, err = r.Search(ctx, snap, strings.Join(terms[:min(len(terms), 24)], " ")+"\n"+q.Ask, Hits)
	}
	return res.Hits, err
}

// score is the share of want that hits hold, and the reciprocal rank of the
// first hit that one of want names (0 when none does, or want is empty).
func score(hits []retrieval.Hit, want []string) (recall, rr float64) {
	if len(want) == 0 {
		return 0, 0
	}
	for _, w := range want {
		if slices.ContainsFunc(hits, func(h retrieval.Hit) bool { return holds(h, w) }) {
			recall++
		}
	}
	if i := slices.IndexFunc(hits, func(h retrieval.Hit) bool {
		return slices.ContainsFunc(want, func(w string) bool { return holds(h, w) })
	}); i >= 0 {
		rr = 1 / float64(i+1)
	}
	return recall / float64(len(want)), rr
}

// holds reports whether hit h is what want names: its file, or with #Name the
// chunk of that file holding symbol Name (a method too: Type.Name).
func holds(h retrieval.Hit, want string) bool {
	path, name, ok := strings.Cut(want, "#")
	return h.Path == path && (!ok || slices.ContainsFunc(strings.Split(h.Symbol, ", "), func(s string) bool {
		return s == name || strings.HasSuffix(s, "."+name)
	}))
}
