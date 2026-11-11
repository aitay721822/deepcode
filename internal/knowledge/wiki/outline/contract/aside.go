package contract

import (
	"cmp"
	"fmt"
	"slices"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	aicontract "deepcode/internal/platform/ai/contract"
)

// Crowd is the most units an aside page of n over pages holds:
// max(6, 2·⌈n/pages⌉).
func Crowd(n, pages int) int { return max(6, 2*((n+max(pages, 1)-1)/max(pages, 1))) }

// grouped reports whether u is a group of units (aside.Group), which a page
// holds whole.
func grouped(u place.Unit) bool { return len(u.Raw) > 1 }

// size is the units u stands for: the units it joins, or itself.
func size(u place.Unit) int { return max(1, len(u.Raw)) }

// weigh is the units units stand for.
func weigh(units []place.Unit) int {
	n := 0
	for _, u := range units {
		n += size(u)
	}
	return n
}

// Aside is an aside's request held to its rules, over its units: 1 to most
// pages, titled; each unit assigned a page's index, a group one page holding
// the units it joins; no page without a unit, or over Crowd of the units most
// pages hold, so the bound counts however few pages the reply gives.
func Aside(ask Ask[AsideReply], units []place.Unit, most int, kind string) aicontract.Turn[AsideReply] {
	return asideTurn(ask, units, most, func(int) int { return Crowd(weigh(units), most) }, kind)
}

// AsideBatch is Aside for a batch of a group planned apart (aside.Apart) in
// at most most pages: its crowd is Crowd over most, not over the reply's
// pages, so most bounds the pages from above and the crowd from below.
func AsideBatch(ask Ask[AsideReply], units []place.Unit, most int, kind string) aicontract.Turn[AsideReply] {
	return asideTurn(ask, units, most, func(int) int { return Crowd(weigh(units), most) }, kind)
}

// asideTurn is an aside's request held to its rules, crowd the most units a
// page holds for the reply's pages, and never less than one unit joins.
func asideTurn(ask Ask[AsideReply], units []place.Unit, most int, crowd func(pages int) int, kind string) aicontract.Turn[AsideReply] {
	seat := func(pages int) int {
		n := crowd(pages)
		for _, u := range units {
			n = max(n, size(u))
		}
		return n
	}
	return aicontract.Turn[AsideReply]{Kind: "aside", Ask: ask, Note: Note,
		Check: func(r AsideReply) []Violation { return checkAside(r, units, most, seat) },
		Fix:   func(r *AsideReply) { fixAside(r, units, most, seat, kind) }}
}

// checkAside checks an aside reply: with no page, that alone, as no unit
// has a page to be assigned.
func checkAside(r AsideReply, units []place.Unit, most int, crowd func(int) int) []Violation {
	if len(r.Pages) == 0 {
		return []Violation{{Rule: "aside.missing", Detail: "no pages: give at least one, and assign each unit a page's index"}}
	}
	var vs []Violation
	if len(r.Pages) > most {
		vs = append(vs, Violation{Rule: "aside.over", Detail: fmt.Sprintf("%d pages, at most %d", len(r.Pages), most)})
	}
	held := make([]int, len(r.Pages))
	for _, u := range units {
		i := r.Assign[u.Key]
		switch {
		case i == nil:
			vs = append(vs, Violation{Rule: "aside.unassigned", Where: u.Key, Detail: fmt.Sprintf("assign.%q is missing: give the index of its page", u.Key)})
		case *i < 0 || *i >= len(r.Pages):
			vs = append(vs, Violation{Rule: "aside.index", Where: u.Key, Detail: fmt.Sprintf("assign.%q is %d, but the pages are numbered 0 to %d", u.Key, *i, len(r.Pages)-1)})
		default:
			held[*i] += size(u)
		}
	}
	for i, p := range r.Pages {
		t := title(p.Title)
		if t == "" {
			vs = append(vs, Violation{Rule: "aside.title", Where: fmt.Sprint(i), Detail: fmt.Sprintf("page %d has an empty title", i)})
		}
		if held[i] == 0 {
			vs = append(vs, Violation{Rule: "aside.empty", Where: t, Detail: fmt.Sprintf("page %d (%q) has no unit assigned", i, t)})
		}
		if n := crowd(len(r.Pages)); held[i] > n {
			vs = append(vs, Violation{Rule: "aside.crowded", Where: t, Detail: fmt.Sprintf("page %q holds %d units, at most %d", t, held[i], n)})
		}
		vs = append(vs, terms(t, p.Terms)...)
	}
	return vs
}

// fixAside is code's fix for what checkAside finds: no page, one titled by
// the kind; pages past most go; a unit unassigned, assigned past the pages,
// or past a crowded page's room goes to the page with the fewest units, or,
// that one full, to a new page: a crowd over the reply's pages always leaves
// one room, and one over most leaves the pages it needs within most; an
// untitled page is titled by its first unit; a page left with none goes.
func fixAside(r *AsideReply, units []place.Unit, most int, crowd func(int) int, kind string) {
	if len(r.Pages) == 0 {
		r.Pages = []Head{{Title: Name(kind)}}
	}
	r.Pages = r.Pages[:min(len(r.Pages), most)]
	n := len(r.Pages)
	byPage := make([][]place.Unit, n)
	held := make([]int, n)
	var loose []place.Unit
	for _, u := range units {
		if i := r.Assign[u.Key]; i != nil && *i >= 0 && *i < n {
			byPage[*i] = append(byPage[*i], u)
			held[*i] += size(u)
		} else {
			loose = append(loose, u)
		}
	}
	limit := crowd(n)
	for i := range byPage {
		for held[i] > limit && len(byPage[i]) > 0 { // the last go first
			j := len(byPage[i]) - 1
			held[i] -= size(byPage[i][j])
			loose, byPage[i] = append(loose, byPage[i][j]), byPage[i][:j]
		}
	}
	// The most units go first, so a group finds its room before the singles
	// fill it; one that still finds none takes the least full page, whose
	// smaller units make room for it and go once more, within a bound.
	slices.SortStableFunc(loose, func(a, b place.Unit) int {
		return cmp.Or(cmp.Compare(size(b), size(a)), cmp.Compare(a.Key, b.Key))
	})
	moves := 0
	for len(loose) > 0 {
		u := loose[0]
		loose = loose[1:]
		best := 0
		for i := range held {
			if held[i] < held[best] {
				best = i
			}
		}
		if held[best]+size(u) > limit && len(byPage) < most {
			byPage, held, r.Pages, best = append(byPage, nil), append(held, 0), append(r.Pages, Head{Terms: []string{}}), len(byPage)
		}
		for held[best]+size(u) > limit && len(byPage[best]) > 0 && moves < 4*len(units)+len(byPage) {
			j := 0
			for i := range byPage[best] {
				if size(byPage[best][i]) < size(byPage[best][j]) {
					j = i
				}
			}
			if size(byPage[best][j]) > size(u) {
				break
			}
			held[best] -= size(byPage[best][j])
			loose = append(loose, byPage[best][j])
			byPage[best] = slices.Delete(byPage[best], j, j+1)
			moves++
		}
		byPage[best], held[best] = append(byPage[best], u), held[best]+size(u)
	}
	pages, at := []Head{}, make([]int, len(r.Pages))
	for i, p := range r.Pages {
		if at[i] = len(pages); len(byPage[i]) == 0 {
			continue
		}
		if title(p.Title) == "" {
			p.Title = Name(byPage[i][0].Key)
		}
		pages = append(pages, p)
	}
	assign := map[string]*int{}
	for i, us := range byPage {
		for _, u := range us {
			assign[u.Key] = &at[i]
		}
	}
	r.Pages, r.Assign = pages, assign
}

// AsidePages are r's pages, each with the units assigned it, in units'
// order, a group's the units it joins.
func AsidePages(r AsideReply, units []place.Unit) []outline.Page {
	out := make([]outline.Page, len(r.Pages))
	for i, p := range r.Pages {
		out[i] = outline.Page{Title: title(p.Title), Terms: outline.Terms(p.Terms), Parent: -1}
	}
	for _, u := range units {
		if i := r.Assign[u.Key]; i != nil && *i >= 0 && *i < len(out) {
			keys := []string{u.Key}
			if grouped(u) {
				keys = u.Raw
			}
			out[*i].Units = append(out[*i].Units, keys...)
		}
	}
	return out
}
