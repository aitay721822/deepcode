package place

import (
	"reflect"
	"slices"
	"testing"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/repo/index/analyzer"
	graphstore "deepcode/internal/repo/index/graphstore"
)

// TestAsideUnits: a guide each, an example by its folder, tests by folder
// (marked, as the folder may be a code unit's too), tooling as one; heaviest
// first.
func TestAsideUnits(t *testing.T) {
	lines := map[string]int{"examples/hello/main.go": 10, "examples/hello/go.mod": 5, "examples/big/main.go": 90, "examples/x.go": 1}
	got := keys(AsideUnits(analyzer.Examples, []string{"examples/big/main.go", "examples/hello/go.mod", "examples/hello/main.go", "examples/x.go"}, lines, nil))
	if want := []string{"examples/big/", "examples/hello/", "examples/x.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("examples %v, want %v", got, want)
	}
	if got := keys(AsideUnits(analyzer.Tests, []string{"a/x_test.go", "a/y_test.go", "b/z_test.go"}, nil, nil)); !reflect.DeepEqual(got, []string{"a/ (tests)", "b/ (tests)"}) {
		t.Errorf("tests %v", got)
	}
	if got := AsideUnits(analyzer.Tooling, []string{"Makefile", ".github/ci.yml"}, nil, nil); len(got) != 1 || len(got[0].Files) != 2 {
		t.Errorf("tooling %+v", got)
	}
}

// TestPlace (G1): every file of a section no page chose goes to one page
// covering its unit: the most imports with the page's chosen files first,
// then the most shared folders, then the fewest placed; a file two pages
// chose stays on both.
func TestPlace(t *testing.T) {
	units := []Unit{
		unit("a/", 2, 0, "a/cfg.json", "a/sub/w.go", "a/sub/z.go", "a/x.go", "a/y.go"),
		unit("b/", 1, 0, "b/1.go", "b/2.go"),
		unit("c/", 1, 0, "c/k.go"),
	}
	pages := []outline.Page{
		{Title: "X", Units: []string{"a/"}, Chosen: []string{"a/x.go", "a/sub/z.go"}},
		{Title: "Z", Units: []string{"a/"}, Chosen: []string{"a/sub/z.go"}},
		{Title: "B", Units: []string{"b/"}, Chosen: []string{"b/1.go"}},
		{Title: "C", Units: []string{"c/"}},
	}
	edges := []graphstore.Edge{{Src: "a/y.go", Dst: "a/sub/z.go"}}
	got := Place(units, pages, edges)
	want := [][]string{{"a/cfg.json", "a/y.go"}, {"a/sub/w.go"}, {"b/2.go"}, {"c/k.go"}}
	for i := range got {
		if !reflect.DeepEqual(got[i].Placed, want[i]) {
			t.Errorf("%s placed %v, want %v", got[i].Title, got[i].Placed, want[i])
		}
	}
	// u.go imports X's file though its folders are Z's; w.go shares Z's folders though Z holds more.
	sub := []Unit{unit("a/", 2, 0, "a/sub/s.go", "a/sub/t.go", "a/sub/u.go", "a/sub/w.go", "a/sub/z.go", "a/x.go")}
	pages = Place(sub, []outline.Page{
		{Title: "X", Units: []string{"a/"}, Chosen: []string{"a/x.go"}},
		{Title: "Z", Units: []string{"a/"}, Chosen: []string{"a/sub/z.go"}},
	}, []graphstore.Edge{{Src: "a/sub/s.go", Dst: "a/sub/z.go"}, {Src: "a/sub/t.go", Dst: "a/sub/z.go"}, {Src: "a/sub/u.go", Dst: "a/x.go"}})
	if want := [][]string{{"a/sub/u.go"}, {"a/sub/s.go", "a/sub/t.go", "a/sub/w.go"}}; !reflect.DeepEqual(pages[0].Placed, want[0]) || !reflect.DeepEqual(pages[1].Placed, want[1]) {
		t.Errorf("by imports, folders, fewest: %v %v, want %v", pages[0].Placed, pages[1].Placed, want)
	}
	if left := Unnamed(pages, []string{"a/x.go", "a/sub/w.go", "c/k.go", "c/k.json", "c/x.go"}); !reflect.DeepEqual(left, []string{"c/k.go", "c/x.go"}) {
		t.Errorf("Unnamed = %v", left)
	}
}

// TestCarry (G16): a renamed file keeps its place under its new path, which
// alone rewrites nothing, and a guide's unit follows it; a deleted one
// leaves, a guide's unit with it, and its page is written again; a page that
// lost every file goes, while one planned with none stays; a head goes with
// its section's last page (P), and with all but one (Core), that one rising
// to the top, unless it holds files (D); one that lost a page (H) is written
// again; a new file of a covered unit, a merged key's part, goes to that
// page; one of a new unit goes, an aside to a page of its kind with the
// fewest units (though another shares its folder), code to a code page it
// imports the most with (never a tests page that imports it), one of a kind
// no page holds to any page it imports; its unit joins the page, which is
// written again; the overview and heads take none.
func TestCarry(t *testing.T) {
	old := []outline.Page{{Title: "Overview", Parent: -1}, {Title: "Core", Parent: -1},
		{Title: "A", Chosen: []string{"a/x.go"}, Units: []string{"a/, k/"}, Parent: 1},
		{Title: "B", Chosen: []string{"b/y.go"}, Units: []string{"b/"}, Parent: 1},
		{Title: "D", Chosen: []string{"d/z.go"}, Units: []string{"d/"}, Parent: -1},
		{Title: "E", Chosen: []string{"e/v.go"}, Units: []string{"e/"}, Parent: 4},
		{Title: "Docs", Placed: []string{"docs/e.md", "docs/f.md", "docs/g.md"}, Units: []string{"docs/e.md", "docs/f.md", "docs/g.md"}, Parent: -1},
		{Title: "T", Placed: []string{"t/a_test.go"}, Units: []string{"t/ (tests)"}, Parent: -1},
		{Title: "Z", Units: []string{"z/"}, Parent: -1}, {Title: "Guide", Placed: []string{"guide/i.md"}, Units: []string{"guide/i.md"}, Parent: -1},
		{Title: "H", Parent: -1}, {Title: "K", Chosen: []string{"u/u.go"}, Units: []string{"u/"}, Parent: 10},
		{Title: "M", Chosen: []string{"v/v.go"}, Units: []string{"v/"}, Parent: 10}, {Title: "N", Chosen: []string{"w/w.go"}, Units: []string{"w/"}, Parent: 10},
		{Title: "P", Parent: -1}, {Title: "Q", Chosen: []string{"x/x.go"}, Units: []string{"x/"}, Parent: 14}}
	owner := map[string]Owner{"a/x2.go": {Key: "a/"}, "d/z.go": {Key: "d/"}, "e/v.go": {Key: "e/"}, "k/r.go": {Key: "k/"}, "n/w.go": {Key: "n/"}, "m/q.go": {Key: "m/"},
		"v/v.go": {Key: "v/"}, "w/w3.go": {Key: "w/"}, "docs/f2.md": {"docs/f2.md", "docs"}, "docs/g.md": {"docs/g.md", "docs"}, "docs/h.md": {"docs/h.md", "docs"},
		"guide/i.md": {"guide/i.md", "docs"}, "t/a_test.go": {"t/ (tests)", "tests"}, "examples/demo/main.go": {"examples/demo/", "examples"}}
	edges := []graphstore.Edge{{Src: "n/w.go", Dst: "d/z.go"}, {Src: "n/w.go", Dst: "e/v.go"}, {Src: "t/a_test.go", Dst: "m/q.go"}, {Src: "examples/demo/main.go", Dst: "v/v.go"}} // ties the heads and T would win
	pages, from, touched := Carry(old, map[int]bool{1: true, 4: true, 10: true, 14: true},
		map[string]string{"a/x.go": "a/x2.go", "docs/f.md": "docs/f2.md", "w/w.go": "w/w3.go"}, map[string]bool{"b/y.go": true, "docs/e.md": true, "u/u.go": true, "x/x.go": true},
		[]string{"docs/h.md", "k/r.go", "n/w.go", "m/q.go", "examples/demo/main.go"}, owner, edges)
	want := []outline.Page{{Title: "Overview", Parent: -1},
		{Title: "A", Chosen: []string{"a/x2.go"}, Placed: []string{"k/r.go"}, Units: []string{"a/, k/"}, Parent: -1},
		{Title: "D", Chosen: []string{"d/z.go"}, Units: []string{"d/"}, Parent: -1},
		{Title: "E", Chosen: []string{"e/v.go"}, Placed: []string{"n/w.go"}, Units: []string{"e/", "n/"}, Parent: 2},
		{Title: "Docs", Placed: []string{"docs/f2.md", "docs/g.md"}, Units: []string{"docs/f2.md", "docs/g.md"}, Parent: -1},
		{Title: "T", Placed: []string{"t/a_test.go"}, Units: []string{"t/ (tests)"}, Parent: -1},
		{Title: "Z", Units: []string{"z/"}, Parent: -1},
		{Title: "Guide", Placed: []string{"guide/i.md", "docs/h.md"}, Units: []string{"guide/i.md", "docs/h.md"}, Parent: -1},
		{Title: "H", Parent: -1}, {Title: "M", Chosen: []string{"v/v.go"}, Placed: []string{"m/q.go", "examples/demo/main.go"}, Units: []string{"v/", "m/", "examples/demo/"}, Parent: 8},
		{Title: "N", Chosen: []string{"w/w3.go"}, Units: []string{"w/"}, Parent: 8}}
	if !reflect.DeepEqual(pages, want) || !reflect.DeepEqual(from, []int{0, 2, 4, 5, 6, 7, 8, 9, 10, 12, 13}) ||
		!reflect.DeepEqual(touched, []bool{false, true, false, true, true, false, false, true, true, true, false}) {
		t.Errorf("Carry:\n%+v\nfrom %v, touched %v", pages, from, touched)
	}
	// A README a recipe put on a code page leaves it a code page.
	pages, _, _ = Carry([]outline.Page{{Title: "Overview", Parent: -1},
		{Title: "Code", Chosen: []string{"c/c.go"}, Placed: []string{"README.md"}, Units: []string{"c/", "README.md#Install"}, Parent: -1},
		{Title: "Guides", Placed: []string{"docs/a.md", "docs/b.md"}, Units: []string{"docs/a.md", "docs/b.md", "docs/c.md"}, Parent: -1}}, nil, nil, nil, []string{"x.md"},
		map[string]Owner{"c/c.go": {Key: "c/"}, "README.md": {"README.md", "docs"}, "docs/a.md": {"docs/a.md", "docs"}, "docs/b.md": {"docs/b.md", "docs"}, "x.md": {"x.md", "docs"}}, nil)
	if !slices.Contains(pages[2].Placed, "x.md") {
		t.Errorf("a guide joined %+v", pages)
	}
	// A page that lost every file (B, G) takes a new one of its own unit, and no other: G goes.
	pages, from, _ = Carry([]outline.Page{{Title: "Overview", Parent: -1}, {Title: "B", Chosen: []string{"b/y.go"}, Units: []string{"b/"}, Parent: -1},
		{Title: "C", Chosen: []string{"c/c.go"}, Placed: []string{"c/d.go"}, Units: []string{"c/"}, Parent: -1}, {Title: "G", Chosen: []string{"g/g.go"}, Units: []string{"g/"}, Parent: -1}},
		nil, nil, map[string]bool{"b/y.go": true, "g/g.go": true}, []string{"b/y2.go", "examples/e/main.go"},
		map[string]Owner{"b/y2.go": {Key: "b/"}, "c/c.go": {Key: "c/"}, "c/d.go": {Key: "c/"}, "examples/e/main.go": {"examples/e/", "examples"}}, nil)
	if !reflect.DeepEqual(from, []int{0, 1, 2}) || !reflect.DeepEqual(pages[1].Placed, []string{"b/y2.go", "examples/e/main.go"}) {
		t.Errorf("emptied pages: from %v, %+v", from, pages)
	}
	// A folder's tests and its code are two units (Owners): a new test goes to
	// the tests page though it imports the code page's file, and a new file of
	// the code to the code page though a test imports it.
	tests := "p/" + TestsMark
	owner = Owners([]Unit{{Key: "p/", Files: []string{"p/a.go", "p/b.go"}}}, []Unit{{Kind: analyzer.Tests, Files: []string{"p/a_test.go", "p/b_test.go"}}}, nil)
	pages, _, _ = Carry([]outline.Page{{Title: "Overview", Parent: -1}, {Title: "Code", Chosen: []string{"p/a.go"}, Units: []string{"p/"}, Parent: -1},
		{Title: "Tests", Placed: []string{"p/a_test.go"}, Units: []string{tests}, Parent: -1}}, nil, nil, nil, []string{"p/b_test.go", "p/b.go"}, owner,
		[]graphstore.Edge{{Src: "p/b_test.go", Dst: "p/a.go"}, {Src: "p/a_test.go", Dst: "p/b.go"}})
	if !reflect.DeepEqual(pages[1].Placed, []string{"p/b.go"}) || !reflect.DeepEqual(pages[2].Placed, []string{"p/a_test.go", "p/b_test.go"}) {
		t.Errorf("a folder's tests and code: %+v", pages)
	}
}

// TestReplan (G7e): a unit growing past a thin page's lines and to a cap of 2
// (capLines), a new thin folder, a new one no code imports (though it imports
// others), or a cap-1 unit gone plans nothing anew; a new imported section's
// worth of code does, and so does a section's unit gone.
func TestReplan(t *testing.T) {
	base := []string{"a/x.go", "c/c.go", "s/s.go"}
	declared := map[string][]string{"a/x.go": {"A", "B"}, "s/s.go": {"S", "T"}, "p/a.go": {"X", "Y", "Z"}, "q/b.go": {"X", "Y", "Z"}}
	edges := []graphstore.Edge{{Src: "a/x.go", Dst: "n/y.go"}, {Src: "a/x.go", Dst: "p/a.go"}, {Src: "q/b.go", Dst: "a/x.go"}}
	was, _ := Raw(base, map[string]int{"a/x.go": 95, "c/c.go": 50, "s/s.go": 300}, declared)
	for name, c := range map[string]struct {
		head []string
		want string
	}{
		"grown":      {base, ""},
		"thin":       {append(slices.Clone(base), "n/y.go"), ""},
		"unimported": {append(slices.Clone(base), "q/b.go"), ""},
		"thin gone":  {[]string{"a/x.go", "s/s.go"}, ""},
		"imported":   {append(slices.Clone(base), "p/a.go", "p/go.mod"), "the new p/ is a section's worth of code that other code imports"},
		"gone":       {[]string{"a/x.go", "c/c.go"}, "s/, which had a section of its own, is gone"},
	} {
		is, _ := Raw(c.head, map[string]int{"a/x.go": 205, "c/c.go": 50, "s/s.go": 300, "n/y.go": 50, "p/a.go": 300, "q/b.go": 300}, declared)
		if got := Replan(was, is, edges); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// TestRecipes (G9): a recipe goes to the page sharing most of its words, the
// overview, heads, full pages and pages of tooling or tests only aside, ties
// to the fewest units; README joins the page's files once.
func TestRecipes(t *testing.T) {
	full := []string{"1", "2", "3", "4", "5", "6"}
	pages := []outline.Page{
		{Title: "Overview", Terms: []string{"persist", "selectors"}},
		{Title: "Persist", Terms: []string{"persist"}},
		{Title: "Store", Terms: []string{"create"}, Units: []string{"src/"}},
		{Title: "Persist middleware", Terms: []string{"persist", "storage"}, Units: full},
		{Title: "Selectors", Terms: []string{"selector"}},
		{Title: "Build and CI", Terms: []string{"github", "actions"}, Units: []string{analyzer.Tooling}},
		{Title: "Tests", Terms: []string{"async", "actions"}, Units: []string{"src/" + TestsMark, analyzer.Tooling}},
		{Title: "Hooks", Terms: []string{"actions"}, Units: []string{"src/hooks/", analyzer.Tooling}},
	}
	got, left := Recipes(pages, "README.md", []string{"Persist middleware", "Selectors", "Store", "Async actions"}, map[int]bool{1: true})
	if len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	if !reflect.DeepEqual(got[4].Units, []string{"README.md#Persist middleware", "README.md#Selectors"}) || !reflect.DeepEqual(got[4].Placed, []string{"README.md"}) {
		t.Errorf("Selectors page: %+v", got[4])
	}
	if !reflect.DeepEqual(got[2].Units, []string{"src/", "README.md#Store"}) || len(got[3].Units) != 6 ||
		len(got[5].Units) != 1 || len(got[6].Units) != 2 || !reflect.DeepEqual(got[7].Units, []string{"src/hooks/", analyzer.Tooling, "README.md#Async actions"}) {
		t.Errorf("Store page %+v, full page %+v, tooling %+v, tests %+v, code and tooling %+v", got[2], got[3], got[5], got[6], got[7])
	}
}
