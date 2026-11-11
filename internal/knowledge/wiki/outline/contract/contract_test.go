package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/platform/ai"
	aicontract "deepcode/internal/platform/ai/contract"
	"deepcode/internal/repo/index/analyzer"
)

// unit is a code unit of files, all of them choices.
func unit(key string, cap int, files ...string) place.Unit {
	return place.Unit{Key: key, Kind: place.Dir, Files: files, Code: files, Cap: cap, Raw: []string{key}}
}

// replying is an Ask that reads text as T, loosely when it breaks format's
// schema, as aicontract.JSON does, each request in turn.
func replying[T any](t *testing.T, format ai.Format[T], texts ...string) (Ask[T], *int) {
	n := 0
	return func(context.Context, ai.Prompt) (T, string, error) {
		text := texts[min(n, len(texts)-1)]
		n++
		v, err := ai.Decode(format, text)
		if err != nil {
			if json.Unmarshal([]byte(text), &v) != nil {
				return v, text, fmt.Errorf("%w", ai.ErrSchema)
			}
			return v, text, fmt.Errorf("%w: %v", aicontract.ErrLoose, err)
		}
		return v, text, nil
	}, &n
}

// rules are the rules vs name, in order.
func rules(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, string(v.Rule))
	}
	return out
}

// TestNoPageCounts (G7f): no prompt or message states a number of pages or
// sections, not even a unit's cap: its bounds are its schema's or code's.
func TestNoPageCounts(t *testing.T) {
	units := []place.Unit{unit("a/", 7, "a/x.go"), unit("b/", 1, "b/z.go")} // no other 7 in the messages
	lines := map[string]int{"a/x.go": 900, "b/z.go": 20}
	parts := []Part{{Key: "1", Units: units}, {Key: "docs", Units: units, Kind: analyzer.Docs}}
	count := regexp.MustCompile(`(?i)\b(\d+|(up to|at most|at least|exactly) \w+)\s+(pages?|sections?)\b`) // not "no two pages share a subject"
	for i, s := range []string{OutlinePrompt, SectionPrompt, AsidePrompt, FlatPrompt, OutlineMessage("", "", parts, lines),
		SectionMessage("", []string{"S"}, "S", units, units[:1], nil, lines), AsideMessage("", []string{"S"}, "Docs", analyzer.Docs, units, nil, lines),
		FlatMessage("", "", units, nil, lines)} {
		if m := count.FindString(s); m != "" || i >= 4 && strings.Contains(s, "7") {
			t.Errorf("text %d states a count or a cap, %q:\n%s", i, m, s)
		}
	}
}

// TestMirror (G26): a reply breaking one rule the schema (with bounds)
// states fails the schema, and Check names exactly that rule; a reply
// breaking none passes both; an extra key fails the schema only.
func TestMirror(t *testing.T) {
	units := []place.Unit{unit("a/", 2, "a/x.go", "a/y.go"), unit("b/", 1, "b/z.go")}
	many := `"` + strings.Repeat(`a/x.go","`, 16) + `a/x.go"`
	thirteen := `"` + strings.Repeat(`t","`, 12) + `t"`
	section, err := SectionFormat(units, true)
	if err != nil {
		t.Fatal(err)
	}
	aside, err := AsideFormat(units, 2)
	if err != nil {
		t.Fatal(err)
	}
	flat, err := FlatFormat(units, true)
	if err != nil {
		t.Fatal(err)
	}
	outlined, err := OutlineFormat([]string{"1", "p1"})
	if err != nil {
		t.Fatal(err)
	}
	const a, b = `{"title":"X","terms":["t"],"files":["a/x.go"]}`, `{"title":"Z","terms":[],"files":["b/z.go"]}`
	const ov, lb = `"overview":{"title":"O","terms":[]},"labels":{"stack":"S","otherFiles":"F"}`, `{"title":"P","terms":[]}`
	for _, c := range []struct {
		kind, text, rule string
	}{
		{"section", `{"a/":[` + a + `],"b/":[` + b + `]}`, ""},
		{"section", `{"a/":[` + a + `]}`, "section.missing"},
		{"section", `{"a/":[` + a + `],"b/":[]}`, "section.missing"},
		{"section", `{"a/":[` + a + `,{"title":"Y","terms":[],"files":["a/y.go"]},{"title":"W","terms":[],"files":["a/y.go"]}],"b/":[` + b + `]}`, "section.over"},
		{"section", `{"a/":[{"title":"X","terms":[],"files":["b/z.go"]}],"b/":[` + b + `]}`, "section.file"},
		{"section", `{"a/":[{"title":"X","terms":[],"files":[]}],"b/":[` + b + `]}`, "section.files"},
		{"section", `{"a/":[{"title":"X","terms":[],"files":[` + many + `]}],"b/":[` + b + `]}`, "section.files"},
		{"section", `{"a/":[{"title":"X","terms":[` + thirteen + `],"files":["a/x.go"]}],"b/":[` + b + `]}`, "terms.over"},
		{"section", `{"a/":[{"title":"X","files":["a/x.go"]}],"b/":[` + b + `]}`, "terms.missing"},
		{"section", `{"a/":[` + a + `],"b/":[` + b + `],"c/":[]}`, "schema.other"},
		{"aside", `{"pages":[` + lb + `],"assign":{"a/":0,"b/":0}}`, ""},
		{"aside", `{"pages":[` + lb + `],"assign":{"a/":0}}`, "aside.unassigned"},
		{"aside", `{"pages":[` + lb + `],"assign":{"a/":null,"b/":0}}`, "aside.unassigned"},
		{"aside", `{"pages":[{"title":"P","terms":[null]}],"assign":{"a/":0,"b/":0}}`, "terms.blank"},
		{"aside", `{"pages":[` + lb + `,{"title":"Q","terms":[]},{"title":"R","terms":[]}],"assign":{"a/":0,"b/":1}}`, "aside.over+aside.empty"},
		{"aside", `{"pages":[` + lb + `],"assign":{"a/":0,"b/":2}}`, "aside.index"},
		{"aside", `{"pages":[` + lb + `],"assign":{"a/":0,"b/":-1}}`, "aside.index"},
		{"aside", `{"pages":[],"assign":{"a/":0,"b/":0}}`, "aside.missing"},
		{"flat", `{` + ov + `,"units":{"a/":[` + a + `],"b/":[` + b + `]}}`, ""},
		{"flat", `{` + ov + `,"units":{"a/":[` + a + `]}}`, "flat.missing"},
		{"flat", `{` + ov + `,"units":{"a/":[` + a + `,{"title":"Y","terms":[],"files":["a/y.go"]},{"title":"W","terms":[],"files":["a/y.go"]}],"b/":[` + b + `]}}`, "flat.over"},
		{"flat", `{` + ov + `,"units":{"a/":[{"title":"X","terms":[],"files":["b/z.go"]}],"b/":[` + b + `]}}`, "flat.file"},
		{"flat", `{` + ov + `,"units":{"a/":[{"title":"X","terms":[],"files":[]}],"b/":[` + b + `]}}`, "flat.files"},
		{"flat", `{"overview":{"title":"O","terms":[]},"units":{"a/":[` + a + `],"b/":[` + b + `]}}`, "outline.label"},
		{"flat", `{"labels":{"stack":"S","otherFiles":"F"},"units":{"a/":[` + a + `],"b/":[` + b + `]}}`, "outline.title+terms.missing"},
		{"outline", `{` + ov + `,"pages":{"1":` + lb + `,"p1":` + lb + `}}`, ""},
		{"outline", `{` + ov + `,"pages":{"1":` + lb + `}}`, "outline.missing"},
		{"outline", `{` + ov + `,"pages":{"1":` + lb + `,"p1":{"title":"P"}}}`, "terms.missing"},
		{"outline", `{"overview":{"title":"O","terms":[` + thirteen + `]},"labels":{"stack":"S","otherFiles":"F"},"pages":{"1":` + lb + `,"p1":` + lb + `}}`, "terms.over"},
	} {
		var schemaErr error
		var named []string
		switch c.kind {
		case "section":
			var v SectionReply
			_, schemaErr = ai.Decode(section, c.text)
			json.Unmarshal([]byte(c.text), &v)
			named = rules(checkUnits("section", v, units))
		case "aside":
			var v AsideReply
			_, schemaErr = ai.Decode(aside, c.text)
			json.Unmarshal([]byte(c.text), &v)
			named = rules(checkAside(v, units, 2))
		case "flat":
			var v FlatReply
			_, schemaErr = ai.Decode(flat, c.text)
			json.Unmarshal([]byte(c.text), &v)
			named = rules(Flat(nil, units).Check(v))
		case "outline":
			var v OutlineReply
			_, schemaErr = ai.Decode(outlined, c.text)
			json.Unmarshal([]byte(c.text), &v)
			named = rules(checkOutline(v.Overview, v.Labels, v.Pages, []string{"1", "p1"}))
		}
		want := strings.Split(c.rule, "+")
		if c.rule == "" || c.rule == "schema.other" {
			want = nil
		}
		if named = slices.Compact(named); (schemaErr != nil) != (c.rule != "") || !reflect.DeepEqual(named, want) {
			t.Errorf("%s %s: schema error %v, check %v; want %v", c.kind, c.text, schemaErr, named, want)
		}
	}
	if got := rules(terms("P", []string{"t", " "})); !reflect.DeepEqual(got, []string{"terms.blank"}) { // the schema lets it pass: Check is stricter
		t.Errorf("a blank term: %v", got)
	}
}

// TestSection (G2–G4): whatever the server does, every unit ends with 1 to
// its cap pages of its own files, and placement puts every file on one: a
// unit the reply leaves out gets a page of its name, pages past the cap go,
// a file of another unit goes; each counted first, after the repair, and as
// residual.
func TestSection(t *testing.T) {
	units := []place.Unit{
		unit("src/WebApp/", 2, "src/WebApp/a.cs", "src/WebApp/b.cs", "src/WebApp/c.cs"),
		unit("src/HybridApp/", 1, "src/HybridApp/MauiProgram.cs"),
	}
	format, err := SectionFormat(units, true)
	if err != nil {
		t.Fatal(err)
	}
	reply := `{"src/WebApp/":[{"title":"A","terms":[],"files":["src/WebApp/a.cs","src/HybridApp/MauiProgram.cs"]},` +
		`{"title":"B","terms":[],"files":["src/WebApp/b.cs"]},{"title":"C","terms":[],"files":["src/WebApp/c.cs"]}]}`
	ask, asked := replying(t, format, reply)
	var counts aicontract.Counts
	r, err := aicontract.Run(context.Background(), ai.Prompt{Dynamic: "plan"}, Section(ask, units), &counts)
	if err != nil || *asked != 2 {
		t.Fatalf("%v after %d requests", err, *asked)
	}
	all := aicontract.Count{First: 1, Repaired: 1, Residual: 1}
	want := map[aicontract.Rule]aicontract.Count{"section.missing": all, "section.over": all, "section.file": all}
	if got := counts.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("counts %v, want %v", got, want)
	}
	pages := place.Place(units, Pages(r, units), nil)
	if len(pages) != 3 || pages[2].Title != "HybridApp" || !reflect.DeepEqual(pages[0].Chosen, []string{"src/WebApp/a.cs"}) {
		t.Errorf("pages %+v", pages)
	}
	if left := place.Unnamed(pages, slices.Concat(units[0].Files, units[1].Files)); len(left) != 0 {
		t.Errorf("unplaced %v", left)
	}
	// A page left with no file goes, unless none of its unit's has one, before
	// the cap: it takes no place of a page with files.
	empty := SectionReply{"src/WebApp/": {{Title: "E", Files: []string{"src/HybridApp/MauiProgram.cs"}}, {Title: "B", Files: []string{"src/WebApp/b.cs"}},
		{Title: "C", Files: []string{"src/WebApp/c.cs"}}}, "src/HybridApp/": {{Title: "H"}}}
	fixUnits(&empty, units)
	if w, h := empty["src/WebApp/"], empty["src/HybridApp/"]; len(w) != 2 || w[0].Title != "B" || w[1].Title != "C" || len(h) != 1 || h[0].Title != "H" {
		t.Errorf("empty pages: %+v", empty)
	}
	// A unit none of whose pages has a file of its own keeps its first pages up
	// to its files the model picks from (Code), so placement gives each a file:
	// code beside configuration counts its code, configuration alone (tooling)
	// all its files, as place.Raw makes them, never none.
	code, asides := place.Raw([]string{"a/x.go", "a/x.yaml", "a/y.yaml", "b/p.go", "b/q.go", "Makefile", ".github/ci.yml"}, nil, nil)
	kept, made := map[string]int{"a/": 1, "b/": 2, analyzer.Tooling: 2}, append(code, asides...)
	for _, u := range made {
		u.Cap, empty = 3, SectionReply{u.Key: {{Title: "P"}, {Title: "Q"}, {Title: "R", Files: []string{"elsewhere.go"}}}}
		fixUnits(&empty, []place.Unit{u})
		pages := place.Place([]place.Unit{u}, Pages(empty, []place.Unit{u}), nil)
		if len(pages) != kept[u.Key] || slices.ContainsFunc(pages, func(p outline.Page) bool { return len(p.Files()) == 0 }) {
			t.Errorf("%s, its pages empty: %+v", u.Key, pages)
		}
	}
	if len(made) != len(kept) {
		t.Errorf("units %+v", made)
	}
	b := code[slices.IndexFunc(code, func(u place.Unit) bool { return u.Key == "b/" })] // fewer pages than files: all stay
	b.Cap, empty = 3, SectionReply{"b/": {{Title: "P"}}}
	if fixUnits(&empty, []place.Unit{b}); len(empty["b/"]) != 1 {
		t.Errorf("one empty page of b/: %+v", empty)
	}
}

// TestAside (G8): pages past the most go; a unit assigned past the pages (at
// their count, or beyond), or not at all, goes to the page with the fewest
// units, as do those past a crowded page's limit; an untitled page takes its
// first unit's name; a page left with none goes. Each fixed reply checks clean.
func TestAside(t *testing.T) {
	g := func(n int) []place.Unit {
		var out []place.Unit
		for i := range n {
			out = append(out, unit(fmt.Sprint("g", i+1), 1))
		}
		return out
	}
	none := []string{}
	at := func(m map[string]int) map[string]*int {
		out := map[string]*int{}
		for k, i := range m {
			out[k] = &i
		}
		return out
	}
	for _, c := range []struct {
		name  string
		units []place.Unit
		most  int
		r     AsideReply
		check []string
		want  [][]string // each page's title, then its units
	}{
		{"over and index", g(3), 2, AsideReply{Pages: []Head{{"P", none}, {"Q", none}, {"", none}}, Assign: at(map[string]int{"g1": 0, "g2": 0, "g3": 3})},
			[]string{"aside.over", "aside.index", "aside.empty", "aside.title", "aside.empty"}, [][]string{{"P", "g1", "g2"}, {"Q", "g3"}}},
		{"untitled and empty", g(2), 3, AsideReply{Pages: []Head{{"", none}, {"B", none}, {"C", none}}, Assign: at(map[string]int{"g1": 0, "g2": 0})},
			[]string{"aside.title", "aside.empty", "aside.empty"}, [][]string{{"g1", "g1", "g2"}}},
		{"crowded", g(8), 3, AsideReply{Pages: []Head{{"A", none}, {"B", none}, {"C", none}}, Assign: at(map[string]int{"g1": 0, "g2": 0, "g3": 0, "g4": 0, "g5": 0, "g6": 0, "g7": 0, "g8": 1})},
			[]string{"aside.crowded", "aside.empty"}, [][]string{{"A", "g1", "g2", "g3", "g4", "g5", "g6"}, {"B", "g8"}, {"C", "g7"}}},
		{"none", g(1), 2, AsideReply{Assign: at(map[string]int{"g1": 0})}, []string{"aside.missing"}, [][]string{{"Documentation", "g1"}}},
		{"null and left out", g(3), 2, AsideReply{Pages: []Head{{"A", none}, {"B", none}}, Assign: map[string]*int{"g1": nil, "g3": at(map[string]int{"x": 0})["x"]}},
			[]string{"aside.unassigned", "aside.unassigned", "aside.empty"}, [][]string{{"A", "g2", "g3"}, {"B", "g1"}}},
	} {
		if got := rules(checkAside(c.r, c.units, c.most)); !reflect.DeepEqual(got, c.check) {
			t.Errorf("%s: check %v, want %v", c.name, got, c.check)
		}
		fixAside(&c.r, c.units, c.most, "docs")
		var got [][]string
		for _, p := range AsidePages(c.r, c.units) {
			got = append(got, append([]string{p.Title}, p.Units...))
		}
		if c.r.Pages[0].Terms = none; !reflect.DeepEqual(got, c.want) || len(checkAside(c.r, c.units, c.most)) != 0 {
			t.Errorf("%s: pages %v, want %v; left %v", c.name, got, c.want, rules(checkAside(c.r, c.units, c.most)))
		}
	}
	if p := AsidePages(AsideReply{Pages: []Head{{"A", none}}, Assign: map[string]*int{"g1": nil}}, g(1)); len(p) != 1 || len(p[0].Units) != 0 { // unfixed: a null index is no page's
		t.Errorf("a null index: %+v", p)
	}
}

// group is a group (aside.Group) keyed key of n guides, key + "1.md" and on.
func group(key string, n int) place.Unit {
	u := place.Unit{Key: key, Kind: analyzer.Docs, Cap: 1}
	for i := range n {
		u.Raw = append(u.Raw, fmt.Sprintf("%s%d.md", key, i+1))
	}
	u.Files, u.Code = u.Raw, u.Raw
	return u
}

// TestSpans (follow-up 2, level 1): a group's pages are mirrored (spans:
// given, no more than its units or most, each a page's index) and fixed: an
// index past the pages or named twice goes, a group given none goes to the
// page with the fewest units, one crowding a page takes the next with the
// fewest; a group on one page puts its units there, one on more none
// (aside.Spread does). Its line names how many units it joins and an even sample
// of them, never their headings, over a budget of names: one group of 1000
// names all, 100 of 20 ten each, spread over them; a message with no group
// says nothing of spans, a request of groups only asks for no "assign".
func TestSpans(t *testing.T) {
	units := []place.Unit{group("d/", 3), unit("g1", 1)}
	format, err := AsideFormat(units, 2)
	if err != nil {
		t.Fatal(err)
	}
	const pq = `"pages":[{"title":"P","terms":[]},{"title":"Q","terms":[]}],"assign":{"g1":0}`
	for text, rule := range map[string]string{`{` + pq + `,"spans":{"d/":[0,1]}}`: "", `{` + pq + `,"spans":{"d/":[]}}`: "aside.unassigned+aside.empty", `{` + pq + `}`: "aside.unassigned+aside.empty",
		`{` + pq + `,"spans":{"d/":[0,1,0]}}`: "aside.span+aside.duplicate", `{` + pq + `,"spans":{"d/":[0,2]}}`: "aside.index+aside.empty", `{` + pq + `,"spans":{"d/":[null]}}`: "aside.index+aside.empty"} {
		var v AsideReply
		_, schemaErr := ai.Decode(format, text)
		json.Unmarshal([]byte(text), &v)
		if named := rules(checkAside(v, units, 2)); (schemaErr != nil) != (rule != "") || strings.Join(named, "+") != rule {
			t.Errorf("%s: schema error %v, check %v; want %s", text, schemaErr, named, rule)
		}
	}
	if only, err := AsideFormat([]place.Unit{group("d/", 3)}, 2); err != nil { // groups only: no "assign"
		t.Errorf("groups only: %v", err)
	} else if _, err := ai.Decode(only, `{"pages":[{"title":"P","terms":[]}],"spans":{"d/":[0]}}`); err != nil {
		t.Errorf("groups only: %v", err)
	}
	none := []string{}
	one, five := 1, 5
	for _, c := range []struct {
		units []place.Unit
		r     AsideReply
		check []string
		want  [][]string // each page's title, then its units
	}{
		{units, AsideReply{Pages: []Head{{"A", none}, {"", none}, {"C", none}}, Assign: map[string]*int{"g1": new(int)}, Spans: map[string][]*int{"d/": {&one, &one, &five}}},
			[]string{"aside.duplicate", "aside.index", "aside.title", "aside.empty"}, [][]string{{"A", "g1"}, {"d", "d/1.md", "d/2.md", "d/3.md"}}},
		{[]place.Unit{group("d/", 20), unit("g1", 1), unit("g2", 1)}, AsideReply{Pages: []Head{{"A", none}, {"B", none}, {"C", none}}, Assign: map[string]*int{"g1": new(int), "g2": new(int)}},
			[]string{"aside.unassigned", "aside.empty", "aside.empty"}, [][]string{{"A", "g1", "g2"}, {"B"}, {"C"}}},
		{[]place.Unit{group("d/", 20), unit("g1", 1)}, AsideReply{Pages: []Head{{"A", none}, {"B", none}, {"C", none}}, Assign: map[string]*int{"g1": &one}, Spans: map[string][]*int{"d/": {new(int)}}},
			[]string{"aside.crowded", "aside.empty"}, [][]string{{"A"}, {"B", "g1"}, {"C"}}},
	} {
		if got := rules(checkAside(c.r, c.units, 3)); !reflect.DeepEqual(got, c.check) {
			t.Errorf("check %v, want %v", got, c.check)
		}
		fixAside(&c.r, c.units, 3, "docs")
		var got [][]string
		for _, p := range AsidePages(c.r, c.units) {
			got = append(got, append([]string{p.Title}, p.Units...))
		}
		if !reflect.DeepEqual(got, c.want) || len(checkAside(c.r, c.units, 3)) != 0 {
			t.Errorf("pages %v, want %v; left %v", got, c.want, rules(checkAside(c.r, c.units, 3)))
		}
	}
	// A page holds ⌈units/pages⌉ of a group: 17 on one of 5 pages (at most 8) take three, not two.
	r := AsideReply{Pages: []Head{{"A", none}, {"B", none}, {"C", none}, {"D", none}, {"E", none}}, Spans: map[string][]*int{"d/": {new(int)}}}
	if fixAside(&r, []place.Unit{group("d/", 17)}, 5, "docs"); len(r.Spans["d/"]) != 3 || len(r.Pages) != 3 {
		t.Errorf("17 units widened to %d pages of %d", len(r.Spans["d/"]), len(r.Pages))
	}
	declared := map[string][]string{"docs/one.md": {"Install"}}
	big := group("docs/", 1000)
	for _, f := range big.Raw {
		declared[f] = []string{"Heading"}
	}
	m := AsideMessage("", nil, "Docs", analyzer.Docs, []place.Unit{big, unit("docs/one.md", 1, "docs/one.md")}, declared, nil)
	if !strings.Contains(m, `"docs/" (a group of 1000 units,`) || !strings.Contains(m, big.Raw[999]+"\n") || strings.Contains(m, "Heading") || !strings.Contains(m, "Install") || !strings.Contains(m, `"spans"`) {
		t.Errorf("one group's line:\n%.600s", m)
	}
	var many []place.Unit
	for i := range 100 {
		many = append(many, group(fmt.Sprintf("g%02d/", i), 20))
	}
	if m := AsideMessage("", nil, "Docs", analyzer.Docs, many, nil, nil); strings.Count(m, ".md") != 1000 || strings.Count(m, "/19.md, …\n") != 100 || strings.Contains(m, "/2.md") {
		t.Errorf("100 groups name %d units:\n%.600s", strings.Count(m, ".md"), m)
	}
	if m := AsideMessage("", nil, "Docs", analyzer.Docs, []place.Unit{unit("a.md", 1, "a.md")}, nil, nil); strings.Contains(m, "spans") {
		t.Errorf("no group, yet:\n%s", m)
	}
}

// TestTitles: a unit's empty title is its name; titles a reply repeats are
// numbered, within a reply (fixUnits) and across a section's requests
// (Distinct), clipped to keep the number; a merged unit's name names each.
func TestTitles(t *testing.T) {
	units := []place.Unit{unit("a/", 3, "a/x.go"), unit("b/", 1, "b/y.go")}
	r := SectionReply{"a/": {{Title: "A", Terms: []string{}, Files: []string{"a/x.go"}}, {Title: "A", Terms: []string{}, Files: []string{"a/x.go"}},
		{Title: "A", Terms: []string{}, Files: []string{"a/x.go"}}}, "b/": {{Title: " ", Terms: []string{}, Files: []string{"b/y.go"}}}}
	if got := rules(checkUnits("section", r, units)); !reflect.DeepEqual(got, []string{"section.duplicate", "section.duplicate", "section.title"}) {
		t.Errorf("check %v", got)
	}
	fixUnits(&r, units)
	if got := []string{r["a/"][0].Title, r["a/"][1].Title, r["a/"][2].Title, r["b/"][0].Title}; !reflect.DeepEqual(got, []string{"A", "A (2)", "A (3)", "b"}) {
		t.Errorf("titles %q", got)
	}
	long := strings.Repeat("x", outline.MaxTitle)
	pages := []outline.Page{{Title: "X"}, {Title: long}, {Title: "X"}, {Title: long}, {Title: "Y"}}
	if n := Distinct(pages); n != 2 || pages[2].Title != "X (2)" || pages[3].Title != long[:outline.MaxTitle-4]+" (2)" {
		t.Errorf("%d renamed: %+v", n, pages)
	}
	for k, want := range map[string]string{"./, middleware/": "Root folder, middleware", "src/a/, src/b/": "a, b", "docs/guide.md": "guide", "tests": "Tests",
		"middleware/" + place.TestsMark: "middleware tests", "./" + place.TestsMark: "Root folder tests"} {
		if got := Name(k); got != want {
			t.Errorf("Name(%q) = %q, want %q", k, got, want)
		}
	}
}

// TestHeads: an outline's head left out or untitled takes its part's name, an
// empty overview title "Overview", an empty label English's.
func TestHeads(t *testing.T) {
	r := OutlineReply{Pages: map[string]Head{"1": {Title: " ", Terms: []string{}}}, Labels: Labels{Stack: "Stack"}}
	turn := Outline(nil, []string{"1", "p1"}, map[string]string{"1": "Core", "p1": "Root folder"})
	if got := rules(turn.Check(r)); !reflect.DeepEqual(got, []string{"outline.title", "terms.missing", "outline.title", "outline.missing", "outline.label"}) {
		t.Errorf("check %v", got)
	}
	turn.Fix(&r)
	if r.Overview.Title != "Overview" || r.Pages["1"].Title != "Core" || r.Pages["p1"].Title != "Root folder" || r.Labels != (Labels{"Stack", English.OtherFiles}) {
		t.Errorf("fixed %+v", r)
	}
}

// TestNote (G27): the repair names each violation in the model's terms,
// never the validator's JSON pointers, and leaves out the Minor ones (too
// many files, terms left out), though not a page with no file.
func TestNote(t *testing.T) {
	units := []place.Unit{unit("src/WebApp/", 2, "src/WebApp/a.cs")}
	r := SectionReply{"src/WebApp/": {{Title: "A", Files: []string{"src/HybridApp/MauiProgram.cs"}}, {Title: "B", Files: []string{"src/WebApp/a.cs"}}, {Title: "C", Files: []string{"src/WebApp/a.cs"}},
		{Title: "D", Terms: []string{}}, {Title: "E", Terms: []string{}, Files: slices.Repeat([]string{"src/WebApp/a.cs"}, maxFiles+1)}}}
	n := Note(checkUnits("section", r, units))
	for _, want := range []string{`"src/WebApp/": 5 pages, at most 2`, `page "A": src/HybridApp/MauiProgram.cs is not a file of "src/WebApp/"`, `page "D": 0 files`} {
		if !strings.Contains(n, want) {
			t.Errorf("note lacks %q:\n%s", want, n)
		}
	}
	if strings.Contains(n, "/properties") || strings.Contains(n, `page "E"`) || strings.Contains(n, "terms is missing") {
		t.Errorf("note quotes a pointer or a Minor violation:\n%s", n)
	}
}

// TestSplit (G12): a section past the limits is asked a unit at a time; a
// unit alone past them is asked with types only.
func TestSplit(t *testing.T) {
	paths := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s/f%04d.go", prefix, i)
		}
		return out
	}
	units := []place.Unit{unit("a/", 2, paths("a", 600)...), unit("b/", 2, paths("b", 1001)...)}
	parts, formats, err := Split(units[:1])
	if err != nil || len(parts) != 1 {
		t.Fatalf("%d parts, %v", len(parts), err)
	}
	parts, formats, err = Split(units)
	if err != nil || len(parts) != 2 || parts[1][0].Key != "b/" {
		t.Fatalf("%d parts, %v", len(parts), err)
	}
	if _, err := ai.Decode(formats[0], `{"a/":[{"title":"X","terms":[],"files":["b/f0000.go"]}]}`); err == nil {
		t.Error("a/'s request has no enum")
	}
	if _, err := ai.Decode(formats[1], `{"b/":[{"title":"X","terms":[],"files":["a/f0000.go"]}]}`); err != nil {
		t.Errorf("b/'s request has bounds: %v", err)
	}
	if _, err := ai.Decode(formats[1], `{}`); err == nil {
		t.Error("b/'s types-only request does not require its key")
	}
	if _, err := SectionFormat(units, true); err == nil {
		t.Error("the whole section fits")
	}
}
