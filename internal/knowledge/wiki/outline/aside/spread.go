package aside

import (
	"fmt"
	"strings"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/contract"
	"deepcode/internal/knowledge/wiki/outline/place"
)

// Ask plans a batch of group g's units in at most k pages, after pages, the
// section's so far.
type Ask func(pages []outline.Page, g place.Unit, batch []place.Unit, k int) ([]outline.Page, error)

// Spread plans each group apart (Apart) in pages of its own, added to pages:
// its units, units' by key, in batches of at most most, path order, each
// asked for one page and its weight's share of the rest of the group's Cap,
// no more than its units: the batches ask for no more than Cap.
func Spread(pages []outline.Page, apart, units []place.Unit, most int, ask Ask) ([]outline.Page, error) {
	fine := map[string]place.Unit{}
	for _, u := range units {
		fine[u.Key] = u
	}
	for _, g := range apart {
		members, total := make([]place.Unit, len(g.Raw)), 0.0
		for j, k := range g.Raw {
			members[j] = fine[k]
			total += members[j].Weight
		}
		cut := batches(members, most)
		for _, batch := range cut {
			w := 0.0
			for _, u := range batch {
				w += u.Weight
			}
			got, err := ask(pages, g, batch, min(len(batch), 1+int(float64(max(0, g.Cap-len(cut)))*w/max(total, 1e-9))))
			if err != nil {
				return nil, err
			}
			pages = append(pages, got...)
		}
	}
	return pages, nil
}

// Message is a level-2 request's message: the aside's (contract.AsideMessage)
// over a batch of group g's units, the section's pages so far, then what
// the batch is; its pages' bound is the schema's, as every request's is.
func Message(about string, titles []string, title, kind string, pages []outline.Page, g place.Unit, batch []place.Unit, declared map[string][]string, lines map[string]int) string {
	var b strings.Builder
	b.WriteString(contract.AsideMessage(about, titles, title, kind, batch, nil, declared, lines))
	if len(pages) > 0 {
		all := make([]string, len(pages))
		for i, p := range pages {
			all[i] = p.Title
		}
		fmt.Fprintf(&b, "\nThe section's pages so far: %s\n", strings.Join(all, " | "))
	}
	fmt.Fprintf(&b, "\nThese are %d of the %d units of %q, planned apart in pages of their own: plan those pages, none taking another page's subject.\n", len(batch), len(g.Raw), g.Key)
	return b.String()
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
