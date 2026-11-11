package aside

import (
	"fmt"
	"slices"
	"strings"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/contract"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/platform/ai"
	aicontract "deepcode/internal/platform/ai/contract"
)

// Reply is a level-2 reply: each unit's page, by its index among its
// group's pages.
type Reply struct {
	Assign map[string]*int `json:"assign"` // nil for a null index, which no page has
}

// Prompt is a level-2 request's instructions, the same on every one.
const Prompt = `You place the units of one group of a technical wiki's section on the pages planned for that group. Reply with the JSON the schema asks for: under "assign", for each listed unit, the index of the page it goes to among the group's pages, the first being 0. A unit goes to the page whose subject it serves.`

// Format asks for each of members' page among span pages, by index.
func Format(members []place.Unit, span int) (ai.Format[Reply], error) {
	assign := map[string]any{}
	for _, u := range members {
		assign[u.Key] = map[string]any{"type": "integer", "minimum": 0, "maximum": span - 1}
	}
	return ai.Dynamic[Reply]("wiki_spread", contract.Object(map[string]any{"assign": contract.Object(assign)}))
}

// Message is a level-2 request's message: the aside's (contract.AsideMessage)
// over members, the section's pages, then the group's, by index, with their
// search terms.
func Message(about string, titles []string, title, kind string, pages []outline.Page, span []int, members []place.Unit, declared map[string][]string, lines map[string]int) string {
	var b strings.Builder
	b.WriteString(contract.AsideMessage(about, titles, title, kind, members, declared, lines))
	all := make([]string, len(pages))
	for i, p := range pages {
		all[i] = p.Title
	}
	fmt.Fprintf(&b, "\nThe section's pages: %s\nThe pages of these units' group, by index:\n", strings.Join(all, " | "))
	for j, i := range span {
		fmt.Fprintf(&b, "%d. %s: %s\n", j, pages[i].Title, strings.Join(pages[i].Terms, ", "))
	}
	return b.String()
}

// Turn is a level-2 request held to its rules: each of members assigned one
// of span pages; one left out, or past them, goes to the one with the fewest.
func Turn(ask contract.Ask[Reply], members []place.Unit, span int) aicontract.Turn[Reply] {
	return aicontract.Turn[Reply]{Kind: "spread", Ask: ask, Note: contract.Note,
		Check: func(r Reply) []contract.Violation {
			var vs []contract.Violation
			for _, u := range members {
				switch i := r.Assign[u.Key]; {
				case i == nil:
					vs = append(vs, contract.Violation{Rule: "spread.unassigned", Where: u.Key, Detail: fmt.Sprintf("assign.%q is missing: give the index of its page", u.Key)})
				case *i < 0 || *i >= span:
					vs = append(vs, contract.Violation{Rule: "spread.index", Where: u.Key, Detail: fmt.Sprintf("assign.%q is %d, but the pages are numbered 0 to %d", u.Key, *i, span-1)})
				}
			}
			return vs
		},
		Fix: func(r *Reply) {
			held, assign := make([]int, span), map[string]*int{}
			var loose []string
			for _, u := range members {
				if i := r.Assign[u.Key]; i != nil && *i >= 0 && *i < span {
					held[*i], assign[u.Key] = held[*i]+1, i
				} else {
					loose = append(loose, u.Key)
				}
			}
			for _, k := range loose {
				i := slices.Index(held, slices.Min(held))
				held[i], assign[k] = held[i]+1, &i
			}
			r.Assign = assign
		}}
}

// Spread puts the units of each group (Group) that r gives two pages or
// more on them, in batches of at most most, path order, each asked by ask,
// adding them to pages (contract.AsidePages); a page left with no unit then
// goes. Those of the groups' pages holding more units than contract.Crowd
// count as aside.crowded, residual, as each batch is checked alone (any
// other page's crowding is level 1's, counted by its turn).
func Spread(pages []outline.Page, r contract.AsideReply, groups, units []place.Unit, most int, ask func(batch []place.Unit, span []int) (Reply, error), counts *aicontract.Counts) ([]outline.Page, error) {
	fine, spanned := map[string]place.Unit{}, map[int]bool{}
	for _, u := range units {
		fine[u.Key] = u
	}
	for _, g := range groups {
		if len(r.Spans[g.Key]) < 2 {
			continue
		}
		var span []int
		for _, i := range r.Spans[g.Key] {
			span, spanned[*i] = append(span, *i), true
		}
		members := make([]place.Unit, len(g.Raw))
		for j, k := range g.Raw {
			members[j] = fine[k]
		}
		for _, batch := range batches(members, most) {
			reply, err := ask(batch, span)
			if err != nil {
				return nil, err
			}
			for _, u := range batch {
				if i := reply.Assign[u.Key]; i != nil && *i >= 0 && *i < len(span) {
					pages[span[*i]].Units = append(pages[span[*i]].Units, u.Key)
				}
			}
		}
	}
	kept, crowded := slices.DeleteFunc(slices.Clone(pages), func(p outline.Page) bool { return len(p.Units) == 0 }), 0
	for i, p := range pages {
		if spanned[i] && len(p.Units) > contract.Crowd(len(units), len(kept)) {
			crowded++
		}
	}
	counts.Residual("aside.crowded", crowded)
	return kept, nil
}

// batches cuts units, in order, into the fewest runs of at most most, of
// even length.
func batches(units []place.Unit, most int) [][]place.Unit {
	n := (len(units) + most - 1) / most
	var out [][]place.Unit
	for k, at := 0, 0; k < n; k++ {
		size := len(units) / n
		if k < len(units)%n {
			size++
		}
		out, at = append(out, units[at:at+size]), at+size
	}
	return out
}
