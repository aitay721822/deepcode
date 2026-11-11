package contract

import (
	"fmt"

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

// Aside is an aside's request held to its rules, over its units: 1 to most
// pages, titled; each unit, a group as one, assigned a page's index; no page
// without a unit, or holding more than Crowd of them over the reply's pages.
func Aside(ask Ask[AsideReply], units []place.Unit, most int, kind string) aicontract.Turn[AsideReply] {
	return asideTurn(ask, units, most, func(pages int) int { return Crowd(len(units), pages) }, kind)
}

// AsideBatch is Aside for a batch of a group planned apart (aside.Apart) in
// at most most pages: its crowd is Crowd over most, not over the reply's
// pages, so most bounds the pages from above and the crowd from below.
func AsideBatch(ask Ask[AsideReply], units []place.Unit, most int, kind string) aicontract.Turn[AsideReply] {
	return asideTurn(ask, units, most, func(int) int { return Crowd(len(units), most) }, kind)
}

// asideTurn is an aside's request held to its rules, crowd the most units a
// page holds for the reply's pages.
func asideTurn(ask Ask[AsideReply], units []place.Unit, most int, crowd func(pages int) int, kind string) aicontract.Turn[AsideReply] {
	return aicontract.Turn[AsideReply]{Kind: "aside", Ask: ask, Note: Note,
		Check: func(r AsideReply) []Violation { return checkAside(r, units, most, crowd) },
		Fix:   func(r *AsideReply) { fixAside(r, units, most, crowd, kind) }}
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
			held[*i]++
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
	byPage := make([][]string, n)
	var loose []string
	for _, u := range units {
		if i := r.Assign[u.Key]; i != nil && *i >= 0 && *i < n {
			byPage[*i] = append(byPage[*i], u.Key)
		} else {
			loose = append(loose, u.Key)
		}
	}
	limit := crowd(n)
	for i := range byPage {
		if len(byPage[i]) > limit {
			loose, byPage[i] = append(loose, byPage[i][limit:]...), byPage[i][:limit]
		}
	}
	for _, k := range loose {
		best := 0
		for i := range byPage {
			if len(byPage[i]) < len(byPage[best]) {
				best = i
			}
		}
		if len(byPage[best]) >= limit {
			byPage, r.Pages, best = append(byPage, nil), append(r.Pages, Head{Terms: []string{}}), len(byPage)
		}
		byPage[best] = append(byPage[best], k)
	}
	pages, at := []Head{}, make([]int, len(r.Pages))
	for i, p := range r.Pages {
		if at[i] = len(pages); len(byPage[i]) == 0 {
			continue
		}
		if title(p.Title) == "" {
			p.Title = Name(byPage[i][0])
		}
		pages = append(pages, p)
	}
	assign := map[string]*int{}
	for i, ks := range byPage {
		for _, k := range ks {
			assign[k] = &at[i]
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
