package place

import (
	"cmp"
	"slices"

	"deepcode/internal/repo/index/analyzer"
	graphstore "deepcode/internal/repo/index/graph/store"
)

// FlatPages is the most pages a wiki planned flat holds: one request, every
// page at the top.
const FlatPages = 8

// Sections groups code units into sections, as indexes into units: a unit
// with cap 2 or more starts one; each cap-1 unit, the lightest first, joins
// the section it shares the most imports with (over all its units, either
// way), then the most kin, then the lighter section, passes repeating until
// one moves nothing; cap-1 units sharing neither with any section group among
// themselves the same way. A group of one cap-1 unit is a top-level page.
// Sections go heaviest first, then the top-level pages, heaviest first.
func Sections(units []Unit, edges []graphstore.Edge) [][]int {
	m, root := links(units, edges), shared(units)
	var groups [][]int
	var loose []int
	for _, i := range byWeight(units) {
		if units[i].Cap >= 2 {
			groups = append(groups, []int{i})
		} else {
			loose = append(loose, i)
		}
	}
	join := func(groups [][]int, loose []int) ([][]int, []int) {
		for moved := true; moved; {
			moved = false
			for k, i := range loose {
				if g := home(units, m, root, groups, i); g >= 0 {
					groups[g] = append(groups[g], i)
					loose = slices.Delete(loose, k, k+1)
					moved = true
					break
				}
			}
		}
		return groups, loose
	}
	groups, loose = join(groups, loose)
	var rest [][]int // the leftovers, among themselves
	for len(loose) > 0 {
		rest = append(rest, []int{loose[0]})
		rest, loose = join(rest, loose[1:])
	}
	var single [][]int
	for _, g := range rest {
		if len(g) == 1 {
			single = append(single, g)
		} else {
			groups = append(groups, g)
		}
	}
	for i := range groups {
		slices.Sort(groups[i])
	}
	weight := func(g []int) float64 {
		w := 0.0
		for _, i := range g {
			w += units[i].Weight
		}
		return w
	}
	heavy := func(a, b []int) int { return cmp.Compare(weight(b), weight(a)) }
	slices.SortStableFunc(groups, heavy)
	slices.SortStableFunc(single, heavy)
	return append(groups, single...)
}

// home is the group unit i joins: the most imports with its units, then the
// most kin with any of them, then the lightest, needing either; -1 for none.
func home(units []Unit, m [][]int, root int, groups [][]int, i int) int {
	best, bestE, bestK, bestW := -1, 0, 0, 0.0
	for g, members := range groups {
		e, k, w := 0, 0, 0.0
		for _, j := range members {
			e, k, w = e+m[i][j], max(k, kin(units[i], units[j], root)), w+units[j].Weight
		}
		if e == 0 && k == 0 {
			continue
		}
		if best < 0 || e > bestE || e == bestE && (k > bestK || k == bestK && w < bestW) {
			best, bestE, bestK, bestW = g, e, k, w
		}
	}
	return best
}

// Shares sets each aside kind's cap, p being the code pages planned: docs
// and examples by their own structure, min(units, 1 + ⌊(w/3) / capLines⌋);
// tests at most ⌈p/10⌉, their weight cap and their units; tooling at most
// ⌈p/10⌉ and its weight cap. Each present takes at least 1.
func Shares(asides []Unit, lines map[string]int, p int) {
	for i, a := range asides {
		asides[i].Cap = Share(a, lines, p)
	}
}

// Share is aside kind a's cap by Shares' rules.
func Share(a Unit, lines map[string]int, p int) int {
	units, byWeight := len(AsideUnits(a.Kind, a.Files, lines, a.Home)), 1+int(a.Weight)/capLines
	switch a.Kind {
	case analyzer.Docs, analyzer.Examples:
		return max(1, min(units, 1+int(a.Weight/3)/capLines))
	case analyzer.Tests:
		return max(1, min((p+9)/10, byWeight, units))
	}
	return max(1, min((p+9)/10, byWeight))
}

// Tied reports whether aside kind k's share follows the code pages.
func Tied(k string) bool { return k == analyzer.Tests || k == analyzer.Tooling }

// asideCaps is the asides' pages: docs and examples at their caps, tests and
// tooling at their shares of p code pages.
func asideCaps(asides []Unit, lines map[string]int, p int) int {
	n := 0
	for _, a := range asides {
		if Tied(a.Kind) {
			n += Share(a, lines, p)
		} else {
			n += a.Cap
		}
	}
	return n
}

// Wanted is the pages a plan of sections over units and asides may take:
// the overview, each section's caps and a head when they sum to 2 or more,
// and the asides' (asideCaps at p = the code caps).
func Wanted(units []Unit, sections [][]int, asides []Unit, lines map[string]int) int {
	n, p := 1, 0
	for _, s := range sections {
		caps := 0
		for _, i := range s {
			caps += units[i].Cap
		}
		if n, p = n+caps, p+caps; caps >= 2 {
			n++
		}
	}
	return n + asideCaps(asides, lines, p)
}

// Flat reports whether a wiki of units and asides is planned flat: 1 + the
// code caps + the asides' (asideCaps at p = the code caps) at most FlatPages.
func Flat(units []Unit, asides []Unit, lines map[string]int) bool {
	p := 0
	for _, u := range units {
		p += u.Cap
	}
	return 1+p+asideCaps(asides, lines, p) <= FlatPages
}

// Ceiling trims the plan to at most most pages, a cost ceiling only, the
// asides' caps set by Shares: while Wanted is past it and a cap is above 1,
// the unit with the most pages per weight, docs and examples among them at a
// third of theirs, loses one; then, every cap 1, the lightest code unit
// merges into its partner (imports, kin, the lighter) until it fits or one is
// left. Nothing ever grows to reach most. The tests' and tooling's caps end
// at their shares of the code caps left. It returns the units, their
// sections, and the pages wanted before.
func Ceiling(units []Unit, asides []Unit, edges []graphstore.Edge, lines map[string]int, most int) ([]Unit, [][]int, int) {
	sections := Sections(units, edges)
	wanted := Wanted(units, sections, asides, lines)
trim:
	for total := wanted; total > most; total = Wanted(units, sections, asides, lines) {
		code, aside, ratio := -1, -1, 0.0
		for i, u := range units {
			if r := float64(u.Cap) / max(u.Weight, 1); u.Cap > 1 && r > ratio {
				code, ratio = i, r
			}
		}
		for i, a := range asides {
			if r := float64(a.Cap) / max(a.Weight/3, 1); !Tied(a.Kind) && a.Cap > 1 && r > ratio {
				aside, ratio = i, r
			}
		}
		switch {
		case aside >= 0:
			asides[aside].Cap--
			continue
		case code >= 0:
			if units[code].Cap--; units[code].Cap > 1 { // it still starts its section: the sections stand
				continue
			}
		// ponytail: each merge sections every unit again, cubic: 500 units
		// take 10 s, 1000 take 90 s; merge a batch per sectioning when
		// repositories that large are planned.
		case len(units) > 1:
			i := byWeight(units)[0]
			units = merge(units, i, partner(units, links(units, edges), shared(units), i, false))
		default:
			break trim
		}
		sections = Sections(units, edges)
	}
	p := 0
	for _, u := range units {
		p += u.Cap
	}
	for i, a := range asides {
		if Tied(a.Kind) {
			asides[i].Cap = Share(a, lines, p)
		}
	}
	return units, sections, wanted
}
