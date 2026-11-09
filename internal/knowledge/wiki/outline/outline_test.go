package outline

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/repo/index/graph"
)

// TestSize: a page for every file of the first 16 and one for every 4, at
// most maxPages; sections from 12 pages on, one for about every 6.
func TestSize(t *testing.T) {
	for _, c := range []struct{ files, max, pages, sections int }{
		{0, 64, 1, 0}, {8, 64, 10, 0}, {10, 64, 12, 2}, {104, 64, 42, 7}, {125, 64, 47, 8}, {432, 64, 64, 11}, {500, 20, 20, 3},
	} {
		if p, s := Size(c.files, c.max); p != c.pages || s != c.sections {
			t.Errorf("Size(%d, %d) = %d, %d; want %d, %d", c.files, c.max, p, s, c.pages, c.sections)
		}
	}
}

// TestSections: the directories importing each other most per file merge
// first, then siblings, into n sections largest first, the asides' among the
// n; docs, examples, tests and tooling follow, a directory of only build and
// configuration files and a README among the tooling, but a long root README
// among the docs.
func TestSections(t *testing.T) {
	parts := map[string]string{"a/x.go": "a", "a/y.go": "a", "b/z.go": "b", "c/w.go": "c", "lib/p/q.go": "lib/p", "lib/r/s.go": "lib/r",
		"a/x_test.go": "", "docs/guide.md": "", "examples/hello/main.go": "", "README.md": "", "build/pom.xml": "build"}
	edges := []graph.DirEdge{{From: "a", To: "b", Count: 5}, {From: "c", To: "a", Count: 2}}
	want := []Section{
		{Dirs: []string{"a", "b", "c"}, Files: []string{"a/x.go", "a/y.go", "b/z.go", "c/w.go"}},
		{Dirs: []string{"lib/p", "lib/r"}, Files: []string{"lib/p/q.go", "lib/r/s.go"}},
		{Kind: "docs", Files: []string{"docs/guide.md"}},
		{Kind: "examples", Files: []string{"examples/hello/main.go"}},
		{Kind: "tests", Files: []string{"a/x_test.go"}},
		{Kind: "tooling", Files: []string{"README.md", "build/pom.xml"}},
	}
	if got := Sections(parts, edges, map[string]int{"README.md": 199}, 6); !reflect.DeepEqual(got, want) {
		t.Errorf("6 sections: %+v", got)
	}
	// A root README as long as a long guide is one; another folder's is not.
	parts["sub/README.md"] = ""
	long := map[string]int{"README.md": 200, "sub/README.md": 900}
	if got := Sections(parts, edges, long, 6); !slices.Equal(got[2].Files, []string{"README.md", "docs/guide.md"}) || !slices.Equal(got[5].Files, []string{"build/pom.xml", "sub/README.md"}) {
		t.Errorf("a long README: %+v", got)
	}
	delete(parts, "sub/README.md")
	if got := Sections(parts, edges, nil, 12); len(got) != 9 || got[0].Dirs[0] != "a" || got[8].Kind != "tooling" {
		t.Errorf("more sections asked than directories: %+v", got)
	}
	if got := Sections(map[string]string{"x_test.go": ""}, nil, nil, 3); len(got) != 1 || got[0].Kind != "tests" {
		t.Errorf("tests alone: %+v", got)
	}
	// A monorepo of thousands of packages: past maxUnits the smallest go in as one, so it plans in no time.
	many, chain := map[string]string{}, []graph.DirEdge{}
	for i := range 3000 {
		d := fmt.Sprintf("packages/p%04d", i)
		many[d+"/index.ts"] = d
		if i%7 == 0 {
			many[d+"/util.ts"] = d
		}
		if i > 0 {
			chain = append(chain, graph.DirEdge{From: d, To: fmt.Sprintf("packages/p%04d", i-1), Count: 1})
		}
	}
	start := time.Now()
	if got := Sections(many, chain, nil, 11); len(got) != 11 || time.Since(start) > 5*time.Second {
		t.Errorf("3000 packages: %d sections in %v", len(got), time.Since(start))
	}
}

// TestFits: a flat wiki fits a repository too small for sections, a
// sectioned one any other.
func TestFits(t *testing.T) {
	if !Fits(false, 8, 64) || Fits(false, 104, 64) || !Fits(true, 104, 64) || Fits(true, 5, 64) {
		t.Error("Fits")
	}
}

// TestBudgets: one page each, then one for each directory of a section, then
// each next page to the section with the fewest for its lines; none past
// twice its files of code and one, nor a page for under linesPerPage lines;
// tests and tooling a few at most.
func TestBudgets(t *testing.T) {
	sections := []Section{{Dirs: []string{"a"}, Files: []string{"a/1.go", "a/2.go", "a/3.go", "a/4.go"}},
		{Dirs: []string{"b"}, Files: []string{"b/1.go", "b/2.go"}}, {Kind: "tests", Files: []string{"t_test.go"}}}
	lines := map[string]int{"a/1.go": 300, "a/2.go": 300, "a/3.go": 300, "a/4.go": 300, "b/1.go": 300, "b/2.go": 300, "t_test.go": 500}
	for pages, want := range map[int][]int{10: {6, 3, 1}, 100: {13, 7, 4}, 2: {1, 1, 1}} {
		if got := Budgets(sections, lines, pages); !slices.Equal(got, want) {
			t.Errorf("%d pages: %v, want %v", pages, got, want)
		}
	}
	lines["t_test.go"] = 40 // too short for a second page, however many are left
	if got := Budgets(sections, lines, 100); !slices.Equal(got, []int{13, 7, 1}) {
		t.Errorf("a short test file: %v", got)
	}
	// Two long files outweigh four short ones; short ones take no page per 100 lines.
	lines = map[string]int{"a/1.go": 50, "a/2.go": 50, "a/3.go": 50, "a/4.go": 50, "b/1.go": 900, "b/2.go": 700, "t_test.go": 40}
	if got := Budgets(sections, lines, 10); !slices.Equal(got, []int{2, 7, 1}) {
		t.Errorf("by lines: %v", got)
	}
	// Each long guide gets a page first, though docs weigh a third.
	guides := []Section{{Dirs: []string{"a"}, Files: []string{"a/1.go", "a/2.go"}}, {Kind: "docs", Files: []string{"Guide.md", "Troubleshooting.md"}}}
	if got := Budgets(guides, map[string]int{"a/1.go": 2000, "a/2.go": 2000, "Guide.md": 300, "Troubleshooting.md": 300}, 6); !slices.Equal(got, []int{4, 2}) {
		t.Errorf("guides: %v", got)
	}
	// A bundle of 50000 lines weighs no more than 2000, and a stylesheet a quarter.
	bundle := []Section{{Dirs: []string{"a"}, Files: []string{"a/app.css", "a/main.go"}}, {Dirs: []string{"b"}, Files: []string{"b/1.go", "b/2.go"}}}
	if got := Budgets(bundle, map[string]int{"a/app.css": 50000, "a/main.go": 1500, "b/1.go": 1000, "b/2.go": 1000}, 6); !slices.Equal(got, []int{3, 3}) {
		t.Errorf("a bundle: %v", got)
	}
	// A section of three directories gets a page for each first.
	three := []Section{{Dirs: []string{"p", "q", "r"}, Files: []string{"p/1.go", "q/1.go", "r/1.go"}}, {Dirs: []string{"big"}, Files: []string{"big/1.go"}}}
	if got := Budgets(three, map[string]int{"p/1.go": 100, "q/1.go": 100, "r/1.go": 100, "big/1.go": 2000}, 6); !slices.Equal(got, []int{4, 2}) {
		t.Errorf("directories first: %v", got)
	}
}

// TestPick: the longest file of each directory in turn, the directories with
// the most lines first, in path order.
func TestPick(t *testing.T) {
	files := []string{"a/x.go", "a/y.go", "a/z.go", "b/q.go", "c/r.go"}
	lines := map[string]int{"a/x.go": 10, "a/y.go": 500, "a/z.go": 20, "b/q.go": 5, "c/r.go": 100}
	if got := Pick(files, lines, 3); !slices.Equal(got, []string{"a/y.go", "b/q.go", "c/r.go"}) {
		t.Errorf("3 of 5: %v", got)
	}
	if got := Pick(files, lines, 4); !slices.Equal(got, []string{"a/y.go", "a/z.go", "b/q.go", "c/r.go"}) {
		t.Errorf("4 of 5: %v", got)
	}
	if got := Pick(files, lines, 9); len(got) != 5 {
		t.Errorf("all: %v", got)
	}
}

// TestUnits: an example is its folder under examples/, a guide its file, tests
// their folder, the largest first; code and tooling have none.
func TestUnits(t *testing.T) {
	lines := map[string]int{"_examples/rest/main.go": 50, "_examples/rest/routes.json": 60, "_examples/fileserver/main.go": 100, "examples/hello.go": 10}
	ex := Section{Kind: "examples", Files: []string{"_examples/rest/main.go", "_examples/rest/routes.json", "_examples/fileserver/main.go", "examples/hello.go"}}
	if got := Units(ex, lines); !slices.Equal(got, []string{"_examples/rest", "_examples/fileserver", "examples/hello.go"}) {
		t.Errorf("examples: %v", got)
	}
	tests := Section{Kind: "tests", Files: []string{"src/test/jmeter/plan.jmx", "src/test/java/a/ATest.java"}}
	if got := Units(tests, map[string]int{"src/test/jmeter/plan.jmx": 900}); !slices.Equal(got, []string{"src/test/jmeter", "src/test/java/a"}) {
		t.Errorf("tests: %v", got)
	}
	if Units(Section{Dirs: []string{"a"}, Files: []string{"a/x.go"}}, nil) != nil || Units(Section{Kind: "tooling", Files: []string{"go.mod"}}, nil) != nil {
		t.Error("code or tooling has units")
	}
}

// TestSpread: a unit no page names goes in front of the query of the page
// given fewest, by its last two parts, at most perPage to a page.
func TestSpread(t *testing.T) {
	pages := []Page{{"REST API", "_examples/rest/main.go", -1}, {"Hello", "hello", -1}}
	units := []string{"_examples/rest", "_examples/fileserver", "src/test/jmeter", "examples/hello.go", "x/y1", "x/y2", "x/y3"} // y3: both are full
	got := Spread(pages, units)
	if got[0].Query != "x/y1 _examples/fileserver _examples/rest/main.go" || got[1].Query != "x/y2 test/jmeter hello" {
		t.Errorf("spread: %+v", got)
	}
	// A name inside a longer word names no unit; root tests have no name.
	got = Spread([]Page{{"Basic-auth and rapid", "q", -1}}, []string{"examples/basic", "tests/api", "."})
	if got[0].Query != "tests/api examples/basic q" {
		t.Errorf("whole words: %+v", got)
	}
}

// TestTitled: a plan's untitled pages are dropped, titles put on one line and
// clipped, queries clipped, and at most limit kept; an outline's sections keep
// their places, untitled ones too, and one without an overview is not taken.
func TestTitled(t *testing.T) {
	plan := func(s string) (p Plan) {
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := plan(`{"pages":[{"title":" Overview ","query":"main"},{"title":"","query":"dropped"},{"title":"Router","query":"mux.go"}]}`).Titled(8); !reflect.DeepEqual(got, []Page{{"Overview", "main", -1}, {"Router", "mux.go", -1}}) {
		t.Fatalf("got %+v", got)
	}
	if lines := plan(`{"pages":[{"title":"Setup\nsucceeded\r\n\tnext","query":"q"}]}`).Titled(8); len(lines) != 1 || lines[0].Title != "Setup succeeded next" {
		t.Fatalf("a title over several lines: %+v", lines)
	}
	if got := plan(`{"pages":[` + strings.Repeat(`{"title":"Page","query":"q"},`, 8) + `{"title":"One too many","query":"q"}]}`).Titled(8); len(got) != 8 {
		t.Fatalf("%d pages planned, want at most 8", len(got))
	}
	long := plan(fmt.Sprintf(`{"pages":[{"title":%q,"query":%q}]}`, strings.Repeat("標", 200), strings.Repeat("q", MaxQuery+100))).Titled(8)
	if len(long) != 1 || len([]rune(long[0].Title)) != MaxTitle || len(long[0].Query) != MaxQuery {
		t.Fatalf("a long title and query: %+v", long)
	}
	o := Outline{Overview: Page{Title: " Overview ", Query: "main"}, Sections: []Page{{Title: "Routing", Query: "mux"}, {Query: "x"}, {Title: "Middleware"}}}
	if overview, sections, ok := o.Heads(1); !ok || overview != (Page{"Overview", "main", -1}) || !reflect.DeepEqual(sections, []Page{{"Routing", "mux", -1}}) {
		t.Fatalf("heads: %+v %+v %v", overview, sections, ok)
	}
	// An untitled section keeps its place, so the next stays with its own.
	if _, sections, _ := o.Heads(3); !reflect.DeepEqual(sections, []Page{{"Routing", "mux", -1}, {"", "x", -1}, {"Middleware", "", -1}}) {
		t.Fatalf("an untitled section: %+v", sections)
	}
	if _, _, ok := (Outline{Sections: o.Sections}).Heads(9); ok {
		t.Fatal("an outline without an overview was taken")
	}
}

// TestFit: a query past MaxQuery loses the separators it starts with and the
// word the cut falls in, a word ending where retrieval.FileNames splits names,
// so every name left is whole; a cut on a separator keeps the word before
// it; a query that is one word is cut where the cut falls.
func TestFit(t *testing.T) {
	var paths []string
	for i := 0; len(strings.Join(paths, " ")) <= MaxQuery; i++ {
		paths = append(paths, fmt.Sprintf("src/Ordering.API/Application/Commands/Command%d.cs", i))
	}
	for _, sep := range []string{" ", ",", ", ", "、"} {
		got := Fit("  " + strings.Join(paths, sep))
		if n := len(strings.FieldsFunc(got, retrieval.Separates)); utf8.RuneCountInString(got) > MaxQuery || got != strings.Join(paths[:n], sep) || n < len(paths)-2 {
			t.Errorf("paths by %q: %d names kept of %d, %q", sep, n, len(paths), got[max(0, len(got)-60):])
		}
	}
	onSpace := strings.Repeat("x", MaxQuery-2) + " a b"
	if got := Fit(onSpace); got != strings.Repeat("x", MaxQuery-2)+" a" {
		t.Errorf("a cut on a space: %q", got[len(got)-4:])
	}
	if got := Fit(" " + strings.Repeat("路", MaxQuery+5)); utf8.RuneCountInString(got) != MaxQuery {
		t.Errorf("one word: %d characters", utf8.RuneCountInString(got))
	}
	if q := "short query"; Fit(q) != q {
		t.Errorf("short: %q", Fit(q))
	}
}
