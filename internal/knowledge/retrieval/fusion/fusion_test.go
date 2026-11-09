package fusion

import (
	"slices"
	"testing"
)

// TestFuse: two lists agreeing near the top outweigh one list's first place
// (a tie goes to the lower id), but not far down: the keyword search's first
// place stays ahead of a chunk it and the symbol search both rank 12th, as
// Gson.fromJson stays ahead of two interfaces matching both; and the symbol
// list's first place counts half the others'.
func TestFuse(t *testing.T) {
	for _, c := range []struct{ fts, vec, sym, named, want []int64 }{
		{[]int64{1, 2, 3}, []int64{3, 4}, []int64{3, 2}, nil, []int64{3, 2, 1, 4}},
		{[]int64{1, 5, 6, 2}, []int64{7, 8, 9, 2}, nil, nil, []int64{2, 1, 7, 5, 8, 6, 9}},
		{[]int64{1, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 2}, nil, []int64{20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 2}, nil,
			[]int64{1, 2, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30}},
		{[]int64{1, 2}, nil, []int64{3}, []int64{4}, []int64{1, 4, 2, 3}}, // a named file counts whole, a symbol half
	} {
		if got := Fuse(c.fts, c.vec, c.sym, c.named); !slices.Equal(got, c.want) {
			t.Errorf("Fuse(%v, %v, %v, %v) = %v, want %v", c.fts, c.vec, c.sym, c.named, got, c.want)
		}
	}
}

// TestSure: each must ranked past the first n takes the last of those places
// not held by another must, the rest moving down; none is lost.
func TestSure(t *testing.T) {
	for _, c := range []struct {
		ids, must []int64
		n         int
		want      []int64
	}{
		{[]int64{1, 2, 3, 4, 5, 6}, []int64{6, 5}, 4, []int64{1, 2, 5, 6, 3, 4}},
		{[]int64{1, 2, 3, 4, 5, 6}, []int64{4, 6}, 4, []int64{1, 2, 4, 6, 3, 5}}, // 4 already in place keeps it
		{[]int64{1, 2, 3}, []int64{2}, 2, []int64{1, 2, 3}},
		{[]int64{1, 2, 3, 4}, []int64{1, 3, 4}, 2, []int64{1, 3, 2, 4}}, // all must: the first n are must
		{[]int64{1, 2, 3}, []int64{1, 2, 3}, 2, []int64{1, 2, 3}},       // the first n all must: the rest wait
		{[]int64{1, 2, 3}, nil, 2, []int64{1, 2, 3}},
		{nil, []int64{1}, 2, nil},
	} {
		if got := Sure(slices.Clone(c.ids), c.must, c.n); !slices.Equal(got, c.want) {
			t.Errorf("Sure(%v, %v, %d) = %v, want %v", c.ids, c.must, c.n, got, c.want)
		}
	}
}
