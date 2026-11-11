// Package aside plans an aside section (docs, examples, tests) of more units
// than one request names, in two levels: one request, with the whole section
// in view, gives each group of them (Group) its pages; then a group given two
// pages or more has its units spread over them (Spread), a request for each
// batch of them.
package aside

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/repo/index/analyzer"
)

// Group bounds an aside's units to most for its request, unchanged when they
// are no more. While there are more, the units the folder holding the most of
// them directly holds (ties: the deeper, then the first by name) become one
// unit, its Raw the keys it joins, keyed by the folder (tests' by its tests'
// key, which joins it), lying in the folder above, so merging goes on up; a
// folder holding one unit only lifts it there. The heaviest come first.
func Group(units []place.Unit, most int) []place.Unit {
	units, most = slices.Clone(units), max(most, 1)
	dir := make([]string, len(units))
	for i, u := range units {
		dir[i] = above(u.Key)
	}
	depth := func(d string) int { return strings.Count(strings.TrimPrefix(d, "./"), "/") } // the root's 0: it comes last
	for len(units) > most {
		held := map[string][]int{}
		for i, d := range dir {
			held[d] = append(held[d], i)
		}
		f := ""
		for d, is := range held {
			deeper := depth(d) - depth(f)
			if f == "" || len(is) > len(held[f]) || len(is) == len(held[f]) && (deeper > 0 || deeper == 0 && d < f) {
				f = d
			}
		}
		in, key := held[f], f
		if units[in[0]].Kind == analyzer.Tests {
			key += place.TestsMark
		}
		if j := slices.IndexFunc(units, func(u place.Unit) bool { return u.Key == key }); j >= 0 && !slices.Contains(in, j) {
			in = append(in, j) // the folder's own tests
		}
		g := units[in[0]]
		if len(in) > 1 {
			g = place.Unit{Key: key, Kind: g.Kind}
			for _, i := range in {
				g.Files, g.Raw, g.Weight = append(g.Files, units[i].Files...), append(g.Raw, units[i].Raw...), g.Weight+units[i].Weight
			}
			slices.Sort(g.Files)
			slices.Sort(g.Raw)
		}
		var kept []place.Unit
		var kd []string
		for i, u := range units {
			if !slices.Contains(in, i) {
				kept, kd = append(kept, u), append(kd, dir[i])
			}
		}
		units, dir = append(kept, g), append(kd, above(f))
	}
	slices.SortStableFunc(units, func(a, b place.Unit) int {
		return cmp.Or(cmp.Compare(b.Weight, a.Weight), strings.Compare(a.Key, b.Key))
	})
	return units
}

// above is the folder holding the file or folder unit key k ("./" at the root).
func above(k string) string {
	d := path.Dir(strings.TrimSuffix(strings.TrimSuffix(k, place.TestsMark), "/"))
	if d == "." {
		return "./"
	}
	return d + "/"
}
