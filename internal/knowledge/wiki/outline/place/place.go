package place

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/repo/index/analyzer"
	graphstore "deepcode/internal/repo/index/graphstore"
)

// TestsMark ends a tests unit's key, after its folder's: a folder's tests and
// its code (Go's and JavaScript's sit together) are two units, never one key.
const TestsMark = " (tests)"

// AsideUnits are aside kind k's units among files, the heaviest first, ties
// in path order: each guide, with the code it shows (home, the docs aside's
// Home), each example (its folder under examples/ or _examples/), each folder
// of tests (TestsMark); tooling is one unit.
func AsideUnits(k string, files []string, lines map[string]int, home map[string]string) []Unit {
	at := map[string]int{}
	var out []Unit
	for _, f := range files {
		key := k
		switch k {
		case analyzer.Docs:
			key = cmp.Or(home[f], f)
		case analyzer.Examples:
			key = example(f)
		case analyzer.Tests:
			key = path.Dir(f) + "/" + TestsMark
		}
		i, ok := at[key]
		if !ok {
			i, at[key] = len(out), len(out)
			out = append(out, Unit{Key: key, Kind: k})
		}
		out[i].Files = append(out[i].Files, f)
	}
	for i := range out {
		slices.Sort(out[i].Files)
		out[i].Code, out[i].Raw = out[i].Files, []string{out[i].Key}
		out[i].Weight = Weigh(out[i].Files, lines, false)
	}
	slices.SortStableFunc(out, func(a, b Unit) int { return cmp.Or(cmp.Compare(b.Weight, a.Weight), strings.Compare(a.Key, b.Key)) })
	return out
}

// example is the folder of example f under examples/ or _examples/, or f
// itself when it lies there directly.
func example(f string) string {
	parts := strings.Split(f, "/")
	for i, p := range parts {
		if (p == "examples" || p == "_examples") && i+2 < len(parts) {
			return strings.Join(parts[:i+2], "/") + "/"
		}
	}
	return f
}

// Place puts every file of a section's units that no page chose on one page
// covering its unit (pages' Units): the most imports with the page's chosen
// files, either way; then the most leading folders shared with one of them;
// then the fewest placed; then plan order. A file two pages chose stays on
// both. An aside page chooses none, so its units' files all go to it.
func Place(units []Unit, pages []outline.Page, edges []graphstore.Edge) []outline.Page {
	near := neighbours(edges)
	chosen := map[string]bool{}
	for _, p := range pages {
		for _, f := range p.Chosen {
			chosen[f] = true
		}
	}
	for _, u := range units {
		var under []int
		for i, p := range pages {
			if slices.Contains(p.Units, u.Key) {
				under = append(under, i)
			}
		}
		for _, f := range u.Files {
			if !chosen[f] && len(under) > 0 {
				i := best(f, under, pages, near, func(p outline.Page) []string { return p.Chosen })
				pages[i].Placed = append(pages[i].Placed, f)
			}
		}
	}
	return pages
}

// neighbours are, for each file, those it imports or is imported by.
func neighbours(edges []graphstore.Edge) map[string][]string {
	near := map[string][]string{}
	for _, e := range edges {
		near[e.Src], near[e.Dst] = append(near[e.Src], e.Dst), append(near[e.Dst], e.Src)
	}
	return near
}

// best is the page of candidates file f goes to: the most imports with its
// files (of), either way; then the most leading folders shared with one of
// them; then the fewest placed; then plan order.
func best(f string, candidates []int, pages []outline.Page, near map[string][]string, of func(outline.Page) []string) int {
	at, bestE, bestF := -1, 0, 0
	for _, i := range candidates {
		e, fs := 0, 0
		for _, c := range of(pages[i]) {
			if slices.Contains(near[f], c) {
				e++
			}
			fs = max(fs, common(strings.Split(path.Dir(f), "/"), strings.Split(path.Dir(c), "/")))
		}
		if at < 0 || e > bestE || e == bestE && (fs > bestF || fs == bestF && len(pages[i].Placed) < len(pages[at].Placed)) {
			at, bestE, bestF = i, e, fs
		}
	}
	return at
}

// maxUnits is the most units a page holds before a recipe passes it by.
const maxUnits = 6

// Recipes places each of recipes, a README's (outline.Recipes), on the page
// whose title, terms and units share the most of its retrieval.Terms, the
// overview (page 0), section heads (heads) and pages of maxUnits units aside,
// ties going to the page with the fewest units, then the first: the recipe
// becomes one of its units, readme + "#" + the recipe, and readme joins its
// placed files. It returns the recipes no page had room for.
func Recipes(pages []outline.Page, readme string, recipes []string, heads map[int]bool) ([]outline.Page, []string) {
	var left []string
	for _, r := range recipes {
		words := retrieval.Terms(r)
		best, most := -1, -1
		for i, p := range pages {
			if i == 0 || heads[i] || len(p.Units) >= maxUnits {
				continue
			}
			terms := retrieval.Terms(p.Title + "\n" + strings.Join(p.Terms, " ") + "\n" + strings.Join(p.Units, " "))
			n := 0
			for _, w := range words {
				if slices.Contains(terms, w) {
					n++
				}
			}
			if n > most || n == most && len(p.Units) < len(pages[best].Units) {
				best, most = i, n
			}
		}
		if best < 0 {
			left = append(left, r)
			continue
		}
		pages[best].Units = append(pages[best].Units, readme+"#"+r)
		if !slices.Contains(pages[best].Files(), readme) {
			pages[best].Placed = append(pages[best].Placed, readme)
		}
	}
	return pages, left
}

// Replan names why a new snapshot's units call for a new plan, "" when they
// do not: a unit all of whose folders are new that would start a section of
// its own (cap 2 or more) and that another unit imports, or one of base's
// all of whose folders are gone that had a section of its own. base and head
// are the raw units (Raw), before the thin merge, so a folder crossing
// LinesPerPage changes nothing; any other change is carried.
func Replan(base, head []Unit, edges []graphstore.Edge) string {
	raw := func(units []Unit) map[string]bool {
		out := map[string]bool{}
		for _, u := range units {
			for _, k := range u.Raw {
				out[k] = true
			}
		}
		return out
	}
	was, is := raw(base), raw(head)
	owner, imported := map[string]int{}, make([]bool, len(head))
	for i, u := range head {
		for _, f := range u.Files {
			owner[f] = i
		}
	}
	for _, e := range edges {
		if a, ok := owner[e.Src]; ok {
			if b, ok := owner[e.Dst]; ok && a != b {
				imported[b] = true
			}
		}
	}
	for i, u := range head {
		if u.Cap >= 2 && !slices.ContainsFunc(u.Raw, func(k string) bool { return was[k] }) && imported[i] {
			return "the new " + u.Key + " is a section's worth of code that other code imports"
		}
	}
	for _, u := range base {
		if u.Cap >= 2 && !slices.ContainsFunc(u.Raw, func(k string) bool { return is[k] }) {
			return u.Key + ", which had a section of its own, is gone"
		}
	}
	return ""
}

// Owner is a file's unit as Carry matches it to pages: a code file's raw
// unit (Raw), which a merge in either snapshot keeps as one of a merged
// key's parts, or an aside file's unit (AsideUnits) and kind.
type Owner struct{ Key, Kind string }

// Owners maps each file of the raw code units and of the asides to its Owner,
// the asides' last: code a guide shows (Illustrations) is in both, and goes
// with its guide.
func Owners(raw, asides []Unit, lines map[string]int) map[string]Owner {
	out := map[string]Owner{}
	for _, u := range raw {
		for _, f := range u.Files {
			out[f] = Owner{Key: u.Key}
		}
	}
	for _, a := range asides {
		for _, u := range AsideUnits(a.Kind, a.Files, lines, a.Home) {
			for _, f := range u.Files {
				out[f] = Owner{Key: u.Key, Kind: a.Kind}
			}
		}
	}
	return out
}

// covers reports whether page p covers unit key k, itself or as a part of a
// merged unit's key.
func covers(p outline.Page, k string) bool {
	return slices.ContainsFunc(p.Units, func(u string) bool { return slices.Contains(strings.Split(u, ", "), k) })
}

// Carry maps a wiki's pages onto a new snapshot: a renamed file keeps its
// place under its new path, and so does a guide's unit; a deleted one leaves
// its page, a guide's unit with it; a new one goes to a page covering its
// unit (owner), by best over its files, else, its unit new, to a page holding
// files of its kind, code or the aside's (there those with the fewest units),
// a README there for a recipe aside, by best, else to any page; its unit
// joins the page. The overview and section heads (heads) take none, nor does
// a page left with no file, but of its own unit. A page that had files and
// has none left goes, unless it is
// the overview or a head; a head holding no file goes with its section's
// last page, and with all but one, that one rising to the top, as a section
// of one page is planned; one that lost a page is written again. It returns
// the pages, the index each had among old, and whether each is written again
// (touched).
func Carry(old []outline.Page, heads map[int]bool, renamed map[string]string, deleted map[string]bool, added []string, owner map[string]Owner, edges []graphstore.Edge) (pages []outline.Page, from []int, touched []bool) {
	pages, changed := make([]outline.Page, len(old)), make([]bool, len(old))
	for i, p := range old {
		keep := func(files []string) []string {
			var out []string
			for _, f := range files {
				if n, ok := renamed[f]; ok {
					out = append(out, n)
				} else if deleted[f] {
					changed[i] = true
				} else {
					out = append(out, f)
				}
			}
			return out
		}
		units := slices.DeleteFunc(slices.Clone(p.Units), func(u string) bool { return deleted[u] }) // a guide's unit is its path
		for j, u := range units {
			if n, ok := renamed[u]; ok {
				units[j] = n
			}
		}
		pages[i] = outline.Page{Title: p.Title, Terms: p.Terms, Chosen: keep(p.Chosen), Placed: keep(p.Placed), Units: units, Parent: p.Parent}
	}
	gone := func(i int) bool { return len(pages[i].Files()) == 0 && len(old[i].Files()) > 0 }
	near := neighbours(edges)
	for _, f := range added {
		o := owner[f]
		var under, kind, all []int
		for i, p := range pages {
			if i == 0 || heads[i] {
				continue
			}
			if covers(p, o.Key) {
				under = append(under, i)
			}
			if gone(i) { // it takes a file of its own unit only
				continue
			}
			if all = append(all, i); slices.ContainsFunc(p.Files(), func(g string) bool {
				h, ok := owner[g]
				return ok && h.Kind == o.Kind && !slices.ContainsFunc(p.Units, func(u string) bool { return strings.HasPrefix(u, g+"#") })
			}) {
				kind = append(kind, i)
			}
		}
		if o.Kind != "" {
			kind = fewest(pages, kind)
		}
		if len(under) == 0 {
			under = kind
		}
		if len(under) == 0 {
			under = all
		}
		if len(under) == 0 {
			continue
		}
		i := best(f, under, pages, near, outline.Page.Files)
		if pages[i].Placed = append(pages[i].Placed, f); o.Key != "" && !covers(pages[i], o.Key) {
			pages[i].Units = append(pages[i].Units, o.Key)
		}
		changed[i] = true
	}
	kept, had, under := make([]bool, len(pages)), map[int]int{}, map[int][]int{}
	for i, p := range pages {
		kept[i] = i == 0 || heads[i] || !gone(i) // a page planned with no file keeps none
		if p.Parent < 0 {
			continue
		}
		if had[p.Parent]++; kept[i] {
			under[p.Parent] = append(under[p.Parent], i)
		}
	}
	for h := range heads {
		switch n := under[h]; {
		case len(pages[h].Files()) > 0:
		case len(n) < 2:
			kept[h] = false
			for _, i := range n {
				pages[i].Parent, changed[i] = -1, true
			}
		case len(n) < had[h]:
			changed[h] = true
		}
	}
	index := map[int]int{}
	for i := range pages {
		if kept[i] {
			index[i] = len(from)
			from, touched = append(from, i), append(touched, changed[i])
		}
	}
	out := make([]outline.Page, len(from))
	for n, i := range from {
		if out[n] = pages[i]; out[n].Parent >= 0 {
			out[n].Parent = index[out[n].Parent]
		}
	}
	return out, from, touched
}

// fewest are those of candidates with the fewest units.
func fewest(pages []outline.Page, candidates []int) []int {
	if len(candidates) == 0 {
		return nil
	}
	units := func(i int) int { return len(pages[i].Units) }
	n := units(slices.MinFunc(candidates, func(a, b int) int { return units(a) - units(b) }))
	return slices.DeleteFunc(slices.Clone(candidates), func(i int) bool { return units(i) > n })
}

// Unnamed are the code files of files (Code) on no page.
func Unnamed(pages []outline.Page, files []string) []string {
	on := map[string]bool{}
	for _, p := range pages {
		for _, f := range p.Files() {
			on[f] = true
		}
	}
	var out []string
	for _, f := range files {
		if Code(f) && !on[f] {
			out = append(out, f)
		}
	}
	return out
}
