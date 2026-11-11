package contract

import (
	"fmt"
	"slices"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	aicontract "deepcode/internal/platform/ai/contract"
)

// crowd is the most units an aside page of n units over pages holds:
// max(6, 2·⌈n/pages⌉).
func crowd(n, pages int) int { return max(6, 2*((n+max(pages, 1)-1)/max(pages, 1))) }

// Aside is an aside's request held to its rules, over its units: 1 to most
// pages, titled; each unit assigned a page's index; no page without a unit
// or past crowd.
func Aside(ask Ask[AsideReply], units []place.Unit, most int, kind string) aicontract.Turn[AsideReply] {
	return aicontract.Turn[AsideReply]{Kind: "aside", Ask: ask, Note: note,
		Check: func(r AsideReply) []Violation { return checkAside(r, units, most) },
		Fix:   func(r *AsideReply) { fixAside(r, units, most, kind) }}
}

// checkAside checks an aside reply: with no page, that alone, as no unit
// has a page to be assigned.
func checkAside(r AsideReply, units []place.Unit, most int) []Violation {
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
		if n := crowd(len(units), len(r.Pages)); held[i] > n {
			vs = append(vs, Violation{Rule: "aside.crowded", Where: t, Detail: fmt.Sprintf("page %q holds %d units, at most %d", t, held[i], n)})
		}
		vs = append(vs, terms(t, p.Terms)...)
	}
	return vs
}

// fixAside is code's fix for what checkAside finds: no page, one titled by
// the kind; pages past most go; an untitled page is titled by its first
// unit; a unit unassigned, or assigned past the pages, goes to the page with
// the fewest units, as do those past a crowded page's limit; a page left
// with none goes.
func fixAside(r *AsideReply, units []place.Unit, most int, kind string) {
	if len(r.Pages) == 0 {
		r.Pages = []Head{{Title: Name(kind)}}
	}
	r.Pages = r.Pages[:min(len(r.Pages), most)]
	byPage := make([][]string, len(r.Pages))
	fewest := func() int {
		best := 0
		for i := range byPage {
			if len(byPage[i]) < len(byPage[best]) {
				best = i
			}
		}
		return best
	}
	var loose []string
	for _, u := range units {
		if i := r.Assign[u.Key]; i != nil && *i >= 0 && *i < len(r.Pages) {
			byPage[*i] = append(byPage[*i], u.Key)
		} else {
			loose = append(loose, u.Key)
		}
	}
	limit := crowd(len(units), len(r.Pages))
	for i := range byPage {
		if len(byPage[i]) > limit {
			loose, byPage[i] = append(loose, byPage[i][limit:]...), byPage[i][:limit]
		}
	}
	for _, k := range loose {
		i := fewest()
		byPage[i] = append(byPage[i], k)
	}
	pages, assign := []Head{}, map[string]*int{}
	for i, p := range r.Pages {
		if len(byPage[i]) == 0 {
			continue
		}
		if title(p.Title) == "" {
			p.Title = Name(byPage[i][0])
		}
		at := len(pages)
		for _, k := range byPage[i] {
			assign[k] = &at
		}
		pages = append(pages, p)
	}
	r.Pages, r.Assign = pages, assign
}

// AsidePages are r's pages, each with the units assigned it, in units' order.
func AsidePages(r AsideReply, units []place.Unit) []outline.Page {
	out := make([]outline.Page, len(r.Pages))
	for i, p := range r.Pages {
		out[i] = outline.Page{Title: title(p.Title), Terms: outline.Terms(p.Terms), Parent: -1}
	}
	for _, u := range units {
		if i := r.Assign[u.Key]; i != nil && *i >= 0 && *i < len(out) && !slices.Contains(out[*i].Units, u.Key) {
			out[*i].Units = append(out[*i].Units, u.Key)
		}
	}
	return out
}
