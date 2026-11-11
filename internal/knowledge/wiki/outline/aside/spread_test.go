package aside

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/contract"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/platform/ai"
	aicontract "deepcode/internal/platform/ai/contract"
	"deepcode/internal/repo/index/analyzer"
)

// TestTurn: a level-2 reply's rules are mirrored (each member given, an
// index among its group's pages) and fixed: a member left out or past them
// goes to the page with the fewest. The message holds the aside's lines for
// the members, the section's pages, and the group's by index.
func TestTurn(t *testing.T) {
	members := place.AsideUnits(analyzer.Docs, []string{"m1", "m2"}, nil)
	format, err := Format(members, 2)
	if err != nil {
		t.Fatal(err)
	}
	for text, rule := range map[string]string{`{"assign":{"m1":0,"m2":1}}`: "", `{"assign":{"m1":0,"m2":2}}`: "spread.index", `{"assign":{"m1":0,"m2":-1}}`: "spread.index",
		`{"assign":{"m1":0}}`: "spread.unassigned", `{"assign":{"m1":0,"m2":null}}`: "spread.unassigned"} {
		var v Reply
		_, schemaErr := ai.Decode(format, text)
		json.Unmarshal([]byte(text), &v)
		var named []string
		for _, v := range Turn(nil, members, 2).Check(v) {
			named = append(named, string(v.Rule))
		}
		if (schemaErr != nil) != (rule != "") || strings.Join(named, "+") != rule {
			t.Errorf("%s: schema error %v, check %v; want %s", text, schemaErr, named, rule)
		}
	}
	four := place.AsideUnits(analyzer.Docs, []string{"m1", "m2", "m3", "m4"}, nil)
	zero, five, below := 0, 5, -1
	r := Reply{Assign: map[string]*int{"m1": &zero, "m2": nil, "m3": &five, "m4": &below}}
	Turn(nil, four, 2).Fix(&r)
	if got := map[string]int{"m1": *r.Assign["m1"], "m2": *r.Assign["m2"], "m3": *r.Assign["m3"], "m4": *r.Assign["m4"]}; !reflect.DeepEqual(got, map[string]int{"m1": 0, "m2": 1, "m3": 0, "m4": 1}) {
		t.Errorf("fixed %v", got)
	}
	pages := []outline.Page{{Title: "Install", Terms: []string{"setup"}}, {Title: "Usage", Terms: []string{"api", "run"}}, {Title: "FAQ"}}
	m := Message("", []string{"S"}, "Docs", analyzer.Docs, pages, []int{2, 1}, place.AsideUnits(analyzer.Docs, []string{"docs/q.md"}, nil), map[string][]string{"docs/q.md": {"Questions"}}, nil)
	for _, want := range []string{"The section's pages: Install | Usage | FAQ\n", "\n0. FAQ: \n1. Usage: api, run\n", `- "docs/q.md" (1 files, 0 lines): Questions`} {
		if !strings.Contains(m, want) || strings.Contains(m, "spans") {
			t.Errorf("message lacks %q, or speaks of spans:\n%s", want, m)
		}
	}
}

// TestSpread: only a group given two pages or more is asked, in even
// batches of at most most, path order, each with its group's pages; a
// member goes to the page its batch gives it; a page left with none goes;
// those of the groups' pages holding more than contract.Crowd, which
// batches checked alone may make, are counted as residual aside.crowded, a
// page at the limit not, nor one level 1 alone crowds.
func TestSpread(t *testing.T) {
	names := func(key string, n int) []string {
		var out []string
		for i := range n {
			out = append(out, fmt.Sprintf("%s%03d.md", key, i))
		}
		return out
	}
	files := append(append(append([]string{"x.md"}, names("a/", 10)...), names("b/", 2)...), names("c/", 250)...)
	units := place.AsideUnits(analyzer.Docs, files, nil)
	groups := []place.Unit{{Key: "a/", Raw: names("a/", 10)}, {Key: "b/", Raw: names("b/", 2)}, {Key: "c/", Raw: names("c/", 250)}}
	idx := func(is ...int) []*int {
		var out []*int
		for _, i := range is {
			out = append(out, &i)
		}
		return out
	}
	r := Reply{}
	reply := map[string][]*int{"a/": idx(0, 2), "b/": idx(1), "c/": idx(1, 2)}
	pages := []outline.Page{{Title: "P0", Units: []string{"x.md"}}, {Title: "P1", Units: names("b/", 2)}, {Title: "P2"}, {Title: "P3"}}
	var asked []string
	var counts aicontract.Counts
	got, err := Spread(pages, contract.AsideReply{Spans: reply}, groups, units, 100, func(batch []place.Unit, span []int) (Reply, error) {
		asked = append(asked, fmt.Sprintf("%d %v %s", len(batch), span, batch[0].Key))
		r.Assign = map[string]*int{}
		for _, u := range batch {
			r.Assign[u.Key] = idx(map[bool]int{true: 1, false: 0}[span[0] == 0])[0]
		}
		return r, nil
	}, &counts)
	var shape []string
	for _, p := range got {
		shape = append(shape, fmt.Sprintf("%s:%d", p.Title, len(p.Units)))
	}
	want := []string{"10 [0 2] a/000.md", "84 [1 2] c/000.md", "83 [1 2] c/084.md", "83 [1 2] c/167.md"}
	if err != nil || !reflect.DeepEqual(asked, want) || !reflect.DeepEqual(shape, []string{"P0:1", "P1:252", "P2:10"}) ||
		!reflect.DeepEqual(counts.Snapshot(), map[aicontract.Rule]aicontract.Count{"aside.crowded": {Residual: 1}}) {
		t.Errorf("asked %v, pages %v, counts %v (%v)", asked, shape, counts.Snapshot(), err)
	}
	// At the limit is not past it, the limit counting the pages kept: 8 of 12 units on one of 3 (at most 8, not 6 of 4).
	counts = aicontract.Counts{}
	twelve := place.AsideUnits(analyzer.Docs, names("g/", 12), nil)
	got, err = Spread([]outline.Page{{Title: "Q0"}, {Title: "Q1"}, {Title: "Q2"}, {Title: "Q3"}}, contract.AsideReply{Spans: map[string][]*int{"g/": idx(0, 1, 2)}},
		[]place.Unit{{Key: "g/", Raw: names("g/", 12)}}, twelve, 100, func(batch []place.Unit, span []int) (Reply, error) {
			r.Assign = map[string]*int{}
			for j, u := range batch {
				k := min(2, max(0, j-6)/2) // 8, 2, 2
				r.Assign[u.Key] = &k
			}
			return r, nil
		}, &counts)
	if err != nil || len(got) != 3 || len(got[0].Units) != 8 || len(counts.Snapshot()) != 0 {
		t.Errorf("at the limit: %d pages, counts %v (%v)", len(got), counts.Snapshot(), err)
	}
	// Batches are the fewest: a group of 100 is one, of 200 two.
	if one, two := batches(make([]place.Unit, 100), 100), batches(make([]place.Unit, 200), 100); len(one) != 1 || len(two) != 2 || len(two[1]) != 100 {
		t.Errorf("batches of 100 and 200: %d, %d", len(one), len(two))
	}
	// A batch's request failing fails the spread.
	if _, err := Spread(got, contract.AsideReply{Spans: map[string][]*int{"g/": idx(0, 1)}}, []place.Unit{{Key: "g/", Raw: names("g/", 12)}}, twelve, 100,
		func([]place.Unit, []int) (Reply, error) { return Reply{}, errors.New("down") }, &counts); err == nil {
		t.Error("a failed batch passed")
	}
	// No group of two pages: 11 units on one of 10 pages (at most 6) is level 1's to count.
	counts, pages = aicontract.Counts{}, []outline.Page{{Title: "P", Units: names("y/", 11)}}
	for i := range 9 {
		pages = append(pages, outline.Page{Title: fmt.Sprint(i), Units: []string{fmt.Sprint(i)}})
	}
	if got, err := Spread(pages, contract.AsideReply{}, nil, make([]place.Unit, 20), 100, nil, &counts); err != nil || len(got) != 10 || len(counts.Snapshot()) != 0 {
		t.Errorf("level 1 only: %d pages, counts %v (%v)", len(got), counts.Snapshot(), err)
	}
}
