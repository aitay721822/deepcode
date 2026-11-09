// Package fusion merges the ranked lists a search runs into one ranking:
// Reciprocal Rank Fusion, and the places a must-have keeps.
package fusion

import (
	"cmp"
	"slices"
)

// k and symbolWeight shape Fuse. At the usual k of 60 with equal weights, a
// chunk two lists rank low (keyword #20 and symbol #5) outranked a list's
// first place: for "how does fromJson deserialize", gson's Gson.fromJson, the
// keyword #1, fell to fifth behind interfaces whose deserialize matched both
// lists. The symbol list counts half: it finds where a name the question
// holds is defined, which answers fewer questions than the text matching
// them, and at a full weight MRR fell on seven of the eight eval corpora,
// whatever the list's order (store.Defining). Chosen on those corpora,
// lexical and with embeddings (README, retrieval eval).
const (
	k            = 20
	symbolWeight = 0.5
)

// Fuse ranks the ids of the keyword, vector, symbol and named lists by
// Reciprocal Rank Fusion, score = Σ weight/(k + rank), the symbol list's
// weight symbolWeight and the others' 1; a tie goes to the lower id.
func Fuse(fts, vec, sym, named []int64) []int64 {
	score := map[int64]float64{}
	add := func(l []int64, weight float64) {
		for rank, id := range l {
			score[id] += weight / float64(k+rank+1)
		}
	}
	add(fts, 1)
	add(vec, 1)
	add(sym, symbolWeight)
	add(named, 1)
	ids := make([]int64, 0, len(score))
	for id := range score {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int64) int {
		return cmp.Or(cmp.Compare(score[b], score[a]), cmp.Compare(a, b))
	})
	return ids
}

// Sure is ids with each of must among the first n: one ranked past them takes
// the last of those places not held by another of must, the ids it displaces
// moving down. must are the best chunks of the files a query names, which
// fusion ranks below most chunks two searches find: eShop's AppHost.cs, a long
// file few of its page's words match, or an example placed in a page about
// another, whose words it shares with none.
func Sure(ids, must []int64, n int) []int64 {
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
