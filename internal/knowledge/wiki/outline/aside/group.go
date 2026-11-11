// Package aside plans an aside section (docs, examples, tests) of more units
// than one request names, in two levels: code joins them into groups (Group)
// and gives each its share of the section's pages by weight (Apart); a group
// of two pages or more is planned apart, in at most that many, a request for
// each batch of its units (Spread); one request, with the whole section in
// view, plans the pages of the rest.
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

// Apart splits units, Group's, of an aside of most pages, its groups asked
// in batches of at most per units (Spread): a group's share of the pages is
// ⌊most·w/W⌋ by weight (w its, W all units'), no more than the units it
// joins; with a page for each of its batches, and two at the least, it is
// planned apart, its Cap the share. While those apart take more than the
// rest leaves them (one page, with any rest), each share is cut in
// proportion, one cut below its least joining the rest. left is most less
// the shares, the rest's pages.
func Apart(units []place.Unit, most, per int) (rest, apart []place.Unit, left int) {
	total := 0.0
	for _, u := range units {
		total += u.Weight
	}
	share, least := map[string]int{}, map[string]int{}
	for _, u := range units {
		least[u.Key] = max(2, (len(u.Raw)+per-1)/per)
		if k := min(len(u.Raw), int(float64(most)*u.Weight/max(total, 1e-9))); k >= least[u.Key] {
			share[u.Key] = k
		}
	}
	for {
		room, sum := most, 0
		if len(share) < len(units) {
			room--
		}
		for _, k := range share {
			sum += k
		}
		if sum <= room || sum == 0 {
			break
		}
		for key, k := range share {
			if k = k * room / sum; k >= least[key] {
				share[key] = k
			} else {
				delete(share, key)
			}
		}
	}
	left = most
	for _, u := range units {
		if k, ok := share[u.Key]; ok {
			u.Cap = k
			apart, left = append(apart, u), left-k
		} else {
			rest = append(rest, u)
		}
	}
	return rest, apart, left
}

// above is the folder holding the file or folder unit key k ("./" at the root).
func above(k string) string {
	d := path.Dir(strings.TrimSuffix(strings.TrimSuffix(k, place.TestsMark), "/"))
	if d == "." {
		return "./"
	}
	return d + "/"
}
