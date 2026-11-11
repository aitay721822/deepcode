package graph

import (
	"context"
	"slices"
	"sort"
	"strings"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/graph/store"
)

// Edges returns the snapshot's import edges between its files.
func (g *Graph) Edges(ctx context.Context, snap int64) ([]store.Edge, error) {
	return g.Store.Edges(ctx, snap)
}

// Dependencies returns up to limit files that paths import, paths themselves
// aside, those more of them import first. Only this direction: a file that
// many import, such as a package's context type, has every caller for an
// importer, and its callers say less of how it works than what it imports.
// ponytail: a barrel file (index.ts re-exporting a folder) brings in all it
// re-exports; rank by the hits' own imports if that crowds retrieval.
func (g *Graph) Dependencies(ctx context.Context, snap int64, paths []string, limit int) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	return g.Store.Dependencies(ctx, snap, paths, limit)
}

// Impact returns the files that a change from snapshot base to snap may make
// read differently beyond their own changed lines: those that import, in
// either snapshot, a changed file that lost a declaration it had (one removed,
// or changed, the file's removal included), changed importers too, as an edit
// elsewhere in one leaves its call of the lost declaration as it was. A change
// inside function bodies reaches no importer, and neither does a new
// declaration: what imported the file before does not use it. A type's whole
// declaration is compared, so a field, method or variant that changes reaches
// it, and a class's range holds its methods' bodies; a function's signature is.
func (g *Graph) Impact(ctx context.Context, base, snap int64, changed map[string]bool) (map[string]bool, error) {
	before, err := g.Store.Declarations(ctx, base, changed)
	if err != nil {
		return nil, err
	}
	after, err := g.Store.Declarations(ctx, snap, changed)
	if err != nil {
		return nil, err
	}
	moved := map[string]bool{}
	for p := range changed {
		if slices.ContainsFunc(before[p], func(d string) bool { return !slices.Contains(after[p], d) }) {
			moved[p] = true
		}
	}
	out := map[string]bool{}
	if len(moved) == 0 {
		return out, nil
	}
	for _, s := range []int64{base, snap} {
		edges, err := g.Store.Edges(ctx, s)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if moved[e.Dst] && e.Src != e.Dst {
				out[e.Src] = true
			}
		}
	}
	return out, nil
}

// DirEdge counts the imports from the files of one directory into another's.
type DirEdge struct {
	From, To string
	Count    int
}

// DirEdges returns the imports between the snapshot's directories, each cut
// to its first depth levels ("." for the root; a run of folders that only
// lead on to one other is one level), most first; tests, examples and docs
// left out.
func (g *Graph) DirEdges(ctx context.Context, snap int64, depth int) ([]DirEdge, error) {
	edges, err := g.Store.Edges(ctx, snap)
	return group(edges, depth), err
}

// group counts edges between directories cut to depth levels, most first,
// leaving out those within one and those of tests, examples and docs.
func group(edges []edge, depth int) []DirEdge { return levels(edges).group(depth) }

// group is group for the tree's edges.
func (t tree) group(depth int) []DirEdge {
	counts := map[[2]string]int{}
	for i := 0; i < len(t.pairs); i += 2 {
		if from, to := t.cut(t.pairs[i], depth), t.cut(t.pairs[i+1], depth); from != to {
			counts[[2]string{from, to}]++
		}
	}
	out := make([]DirEdge, 0, len(counts))
	for k, n := range counts {
		out = append(out, DirEdge{From: k[0], To: k[1], Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.From < b.From || a.From == b.From && a.To < b.To
	})
	return out
}

// secondary reports a test, example or docs file, which retrieval ranks lower
// too: what such a file imports shows how the code is used, not how its parts
// depend on each other, and examples alone can crowd a diagram of them.
var secondary = analyzer.Secondary

// tree is the directories of the files that edges link, tests, examples and
// docs aside, cut by levels: a level is a directory that holds one of the
// files or leads to two or more, so folders that only lead on to one other
// count once, as an editor's compact folders show them: Java's
// src/main/java/org/acme is one level, not five. The trunk, the first
// directory under which all files are (src/ when only it has any), is none:
// a level of one directory draws nothing.
type tree struct {
	pairs []string // the linked files, from and to
	trunk string
	holds map[string]bool
	below map[string]map[string]bool
}

// levels builds the tree of edges' files.
func levels(edges []edge) tree {
	t := tree{holds: map[string]bool{}, below: map[string]map[string]bool{}}
	for _, e := range edges {
		if !secondary(e.Src) && !secondary(e.Dst) {
			t.pairs = append(t.pairs, e.Src, e.Dst)
		}
	}
	for _, f := range t.pairs {
		d := dir(f)
		if t.holds[d] {
			continue
		}
		t.holds[d] = true
		for p := d; p != ""; p = dir(p) {
			if t.below[dir(p)] == nil {
				t.below[dir(p)] = map[string]bool{}
			}
			t.below[dir(p)][p] = true
		}
	}
	for !t.holds[t.trunk] && len(t.below[t.trunk]) == 1 {
		for c := range t.below[t.trunk] {
			t.trunk = c
		}
	}
	return t
}

// stop reports whether directory d ends a level.
func (t tree) stop(d string) bool {
	return len(d) > len(t.trunk) && (t.holds[d] || len(t.below[d]) > 1)
}

// cut is the directory of file p cut to its first depth levels, "." at the root.
func (t tree) cut(p string, depth int) string {
	d := dir(p)
	if d == "" {
		return "."
	}
	for i, n := 0, 0; ; i++ {
		j := strings.IndexByte(d[i:], '/')
		if j < 0 {
			return d
		}
		if i += j; t.stop(d[:i]) {
			if n++; n == depth {
				return d[:i]
			}
		}
	}
}

// short names directory d with each run of three or more folders that one
// level or the trunk folds shown as its first and last: gson/…/gson/internal
// for gson/src/main/java/com/google/gson/internal.
func (t tree) short(d string) string {
	parts := strings.Split(d, "/")
	var out []string
	from := 0
	for i := range parts {
		if p := strings.Join(parts[:i+1], "/"); i == len(parts)-1 || p == t.trunk || t.stop(p) {
			if run := parts[from : i+1]; len(run) > 2 {
				out = append(out, run[0], "…", run[len(run)-1])
			} else {
				out = append(out, run...)
			}
			from = i + 1
		}
	}
	return strings.Join(out, "/")
}
