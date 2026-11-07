// Package eval measures retrieval on questions whose answers are known: each
// repository is indexed at a pinned commit, and each question is searched the
// way chat searches it. eval_test.go holds recall@10 and MRR at or above the
// floors recorded with the questions (cases.go); the bench (test/bench)
// reports them beside its other numbers.
//
// deepcode's own questions run with every go test. The other repositories'
// need their history: set DEEPCODE_EVAL_GIT to a folder holding a bare clone
// of each, named after its corpus (git clone --bare <url> chi.git).
package eval

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"deepcode/internal/knowledge/retrieval"
)

// Hits is how many results a question reads: as many as chat's evidence.
const Hits = 10

type Question struct {
	Ask   string   // as a user asks it
	Terms string   // for a question none of whose words the code holds: the model's search terms for it, frozen
	Want  []string // the files that answer it
}

type Corpus struct {
	Name, URL, SHA string  // URL: where the clone comes from; "" for this repository
	Recall, MRR    float64 // the floors
	Questions      []Question
}

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
		files, err := search(ctx, r, snap, q)
		if err != nil {
			return res, err
		}
		rec, rr := score(files, q.Want)
		res.Recall, res.MRR = res.Recall+rec, res.MRR+rr
		if rec < 1 {
			res.Misses = append(res.Misses, fmt.Sprintf("%q: want %v, found %v", q.Ask, q.Want, files))
		}
	}
	n := float64(max(len(c.Questions), 1))
	res.Recall, res.MRR = res.Recall/n, res.MRR/n
	return res, nil
}

// search returns the files of q's hits in order, searched as chat's search
// does it: the question alone, then, when none of its words match the code,
// with the model's terms in front.
// ponytail: a copy of chat.Service.search's rule, without its model; follow
// it if that rule changes.
func search(ctx context.Context, r *retrieval.Retriever, snap int64, q Question) ([]string, error) {
	res, err := r.Search(ctx, snap, q.Ask, Hits)
	if err == nil && res.FTS == 0 {
		if q.Terms == "" {
			return nil, fmt.Errorf("%q matches no word of the code: freeze the model's search terms for it", q.Ask)
		}
		terms := strings.Fields(q.Terms)
		res, err = r.Search(ctx, snap, strings.Join(terms[:min(len(terms), 24)], " ")+"\n"+q.Ask, Hits)
	}
	if err != nil {
		return nil, err
	}
	files := make([]string, len(res.Hits))
	for i, h := range res.Hits {
		files[i] = h.Path
	}
	return files, nil
}

// score is the share of want that files hold, and the reciprocal rank of the
// first of files in want (0 when none is, or want is empty).
func score(files, want []string) (recall, rr float64) {
	if len(want) == 0 {
		return 0, 0
	}
	for _, w := range want {
		if slices.Contains(files, w) {
			recall++
		}
	}
	if i := slices.IndexFunc(files, func(f string) bool { return slices.Contains(want, f) }); i >= 0 {
		rr = 1 / float64(i+1)
	}
	return recall / float64(len(want)), rr
}
