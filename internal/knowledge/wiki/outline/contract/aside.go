package contract

import (
	"fmt"
	"slices"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	aicontract "deepcode/internal/platform/ai/contract"
)

// Crowd is the most units an aside page of n over pages holds:
// max(6, 2·⌈n/pages⌉).
func Crowd(n, pages int) int { return max(6, 2*((n+max(pages, 1)-1)/max(pages, 1))) }

// grouped reports whether u is a group of units (aside.Group), which a reply
// gives its pages under "spans", not "assign".
func grouped(u place.Unit) bool { return len(u.Raw) > 1 }

// count is how many units units are, a group the units it joins.
func count(units []place.Unit) int {
	n := 0
	for _, u := range units {
		n += max(1, len(u.Raw))
	}
	return n
}

// Aside is an aside's request held to its rules, over its units: 1 to most
// pages, titled; each unit assigned a page's index, each group 1 to as many
// as it has units (no more than most), none twice; no page without a unit
// or past Crowd, a group counting its units spread evenly over its pages.
func Aside(ask Ask[AsideReply], units []place.Unit, most int, kind string) aicontract.Turn[AsideReply] {
	return aicontract.Turn[AsideReply]{Kind: "aside", Ask: ask, Note: Note,
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
		if grouped(u) {
			vs = append(vs, checkSpan(r.Spans[u.Key], u, len(r.Pages), most)...)
			on := clean(r.Spans[u.Key], len(r.Pages), len(u.Raw))
			for _, i := range on {
				held[i] += share(len(u.Raw), len(on))
			}
			continue
		}
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
		if n := Crowd(count(units), len(r.Pages)); held[i] > n {
			vs = append(vs, Violation{Rule: "aside.crowded", Where: t, Detail: fmt.Sprintf("page %q holds %d units, at most %d", t, held[i], n)})
		}
		vs = append(vs, terms(t, p.Terms)...)
	}
	return vs
}

// checkSpan checks group g's pages, span, of pages: given, no more than g's
// units or most, each a page's index, none twice.
func checkSpan(span []*int, g place.Unit, pages, most int) []Violation {
	var vs []Violation
	switch {
	case len(span) == 0:
		vs = append(vs, Violation{Rule: "aside.unassigned", Where: g.Key, Detail: fmt.Sprintf("spans.%q is missing: give the indexes of its pages", g.Key)})
	case len(span) > min(most, len(g.Raw)):
		vs = append(vs, Violation{Rule: "aside.span", Where: g.Key, Detail: fmt.Sprintf("spans.%q names %d pages, at most %d", g.Key, len(span), min(most, len(g.Raw)))})
	}
	for j, i := range span {
		switch {
		case i == nil || *i < 0 || *i >= pages:
			vs = append(vs, Violation{Rule: "aside.index", Where: g.Key, Detail: fmt.Sprintf("spans.%q holds an index past the pages, numbered 0 to %d", g.Key, pages-1)})
		case slices.ContainsFunc(span[:j], func(k *int) bool { return k != nil && *k == *i }):
			vs = append(vs, Violation{Rule: "aside.duplicate", Where: g.Key, Detail: fmt.Sprintf("spans.%q names page %d twice", g.Key, *i)})
		}
	}
	return vs
}

// clean is span's indexes of pages, each once, the first room of them.
func clean(span []*int, pages, room int) []int {
	var out []int
	for _, i := range span {
		if i != nil && *i >= 0 && *i < pages && !slices.Contains(out, *i) && len(out) < room {
			out = append(out, *i)
		}
	}
	return out
}

// share is the units each of on pages holds of a group of n: ⌈n/on⌉.
func share(n, on int) int { return (n + on - 1) / on }

// fixAside is code's fix for what checkAside finds: no page, one titled by
// the kind; pages past most go; a unit unassigned, or assigned past the
// pages, goes to the page with the fewest units, as does a group given no
// page (its pages cleaned of those past them or named twice); a group on a
// crowded page takes the next page with the fewest while it has fewer than
// its units, then a unit past a crowded page's room goes to the page with
// the fewest; an untitled page is titled by its first unit; a page left
// with none goes.
func fixAside(r *AsideReply, units []place.Unit, most int, kind string) {
	if len(r.Pages) == 0 {
		r.Pages = []Head{{Title: Name(kind)}}
	}
	r.Pages = r.Pages[:min(len(r.Pages), most)]
	n := len(r.Pages)
	byPage, held, spans := make([][]string, n), make([]int, n), map[string][]int{}
	fewest := func(but []int) int {
		best := -1
		for i := range held {
			if !slices.Contains(but, i) && (best < 0 || held[i] < held[best]) {
				best = i
			}
		}
		return best
	}
	hold := func(g place.Unit, by int) {
		for _, i := range spans[g.Key] {
			held[i] += by * share(len(g.Raw), len(spans[g.Key]))
		}
	}
	var loose []string
	var groups []place.Unit
	for _, u := range units {
		if grouped(u) {
			spans[u.Key], groups = clean(r.Spans[u.Key], n, len(u.Raw)), append(groups, u)
			hold(u, 1)
		} else if i := r.Assign[u.Key]; i != nil && *i >= 0 && *i < n {
			byPage[*i], held[*i] = append(byPage[*i], u.Key), held[*i]+1
		} else {
			loose = append(loose, u.Key)
		}
	}
	limit := Crowd(count(units), n)
	for _, g := range groups {
		if len(spans[g.Key]) == 0 {
			spans[g.Key] = []int{fewest(nil)}
			hold(g, 1)
		}
		for len(spans[g.Key]) < min(n, len(g.Raw)) && slices.ContainsFunc(spans[g.Key], func(i int) bool { return held[i] > limit }) {
			hold(g, -1)
			spans[g.Key] = append(spans[g.Key], fewest(spans[g.Key]))
			hold(g, 1)
		}
	}
	for i := range byPage {
		if room := max(0, limit-(held[i]-len(byPage[i]))); len(byPage[i]) > room {
			loose, held[i] = append(loose, byPage[i][room:]...), held[i]-(len(byPage[i])-room)
			byPage[i] = byPage[i][:room]
		}
	}
	for _, k := range loose {
		i := fewest(nil)
		byPage[i], held[i] = append(byPage[i], k), held[i]+1
	}
	pages, at := []Head{}, make([]int, n)
	for i, p := range r.Pages {
		if at[i] = len(pages); held[i] == 0 {
			continue
		}
		if title(p.Title) == "" && len(byPage[i]) > 0 {
			p.Title = Name(byPage[i][0])
		} else if title(p.Title) == "" { // a group's page only
			p.Title = Name(groups[slices.IndexFunc(groups, func(g place.Unit) bool { return slices.Contains(spans[g.Key], i) })].Key)
		}
		pages = append(pages, p)
	}
	assign := map[string]*int{}
	for i, ks := range byPage {
		for _, k := range ks {
			assign[k] = &at[i]
		}
	}
	r.Pages, r.Assign, r.Spans = pages, assign, nil
	for _, g := range groups {
		if r.Spans == nil {
			r.Spans = map[string][]*int{}
		}
		for _, i := range spans[g.Key] {
			r.Spans[g.Key] = append(r.Spans[g.Key], &at[i])
		}
	}
}

// AsidePages are r's pages, each with the units assigned it, in units'
// order, and the units of a group given one page; a group given more has its
// units put on them by aside.Spread.
func AsidePages(r AsideReply, units []place.Unit) []outline.Page {
	out := make([]outline.Page, len(r.Pages))
	for i, p := range r.Pages {
		out[i] = outline.Page{Title: title(p.Title), Terms: outline.Terms(p.Terms), Parent: -1}
	}
	for _, u := range units {
		if i, s := r.Assign[u.Key], r.Spans[u.Key]; !grouped(u) && i != nil && *i >= 0 && *i < len(out) {
			out[*i].Units = append(out[*i].Units, u.Key)
		} else if grouped(u) && len(s) == 1 && s[0] != nil && *s[0] >= 0 && *s[0] < len(out) {
			out[*s[0]].Units = append(out[*s[0]].Units, u.Raw...)
		}
	}
	return out
}
