package outline

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
// first, then siblings, into n sections largest first; tests, examples and
// docs are the last.
func TestSections(t *testing.T) {
	parts := map[string]string{"a/x.go": "a", "a/y.go": "a", "b/z.go": "b", "c/w.go": "c", "lib/p/q.go": "lib/p", "lib/r/s.go": "lib/r", "a/x_test.go": ""}
	edges := []graph.DirEdge{{From: "a", To: "b", Count: 5}, {From: "c", To: "a", Count: 2}}
	want := []Section{
		{Dirs: []string{"a", "b", "c"}, Files: []string{"a/x.go", "a/y.go", "b/z.go", "c/w.go"}},
		{Dirs: []string{"lib/p", "lib/r"}, Files: []string{"lib/p/q.go", "lib/r/s.go"}},
		{Dirs: []string{""}, Files: []string{"a/x_test.go"}},
	}
	if got := Sections(parts, edges, 3); !reflect.DeepEqual(got, want) {
		t.Errorf("3 sections: %+v", got)
	}
	if got := Sections(parts, edges, 9); len(got) != 6 || got[0].Dirs[0] != "a" || got[5].Dirs[0] != "" {
		t.Errorf("more sections asked than directories: %+v", got)
	}
	if got := Sections(map[string]string{"x_test.go": ""}, nil, 3); len(got) != 1 {
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
	if got := Sections(many, chain, 11); len(got) != 11 || time.Since(start) > 5*time.Second {
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

// TestBudgets: one page each, then each next page to the section with the
// fewest for its lines, tests counting half, none past twice its files.
func TestBudgets(t *testing.T) {
	sections := []Section{{Files: []string{"a", "b", "c", "d"}}, {Files: []string{"e", "f"}}, {Dirs: []string{""}, Files: []string{"g"}}}
	for pages, want := range map[int][]int{10: {6, 3, 1}, 100: {8, 4, 2}, 2: {1, 1, 1}} {
		if got := Budgets(sections, nil, pages); !slices.Equal(got, want) {
			t.Errorf("%d pages: %v, want %v", pages, got, want)
		}
	}
	// Two long files outweigh four short ones, up to twice their files.
	if got := Budgets(sections, map[string]int{"a": 50, "b": 50, "c": 50, "d": 50, "e": 900, "f": 700, "g": 40}, 10); !slices.Equal(got, []int{5, 4, 1}) {
		t.Errorf("by lines: %v", got)
	}
	// A bundle of 50000 lines weighs no more than 2000.
	if got := Budgets(sections, map[string]int{"a": 600, "b": 600, "c": 600, "d": 600, "e": 50000, "f": 1, "g": 1}, 6); !slices.Equal(got, []int{3, 2, 1}) {
		t.Errorf("a bundle: %v", got)
	}
}

// TestParse: plans are read past reasoning and prose, untitled pages dropped,
// titles put on one line and clipped, queries clipped, and at most limit kept.
func TestParse(t *testing.T) {
	out := "<think>let me plan</think>Here is the plan:\n```json\n" +
		`{"pages":[{"title":" Overview ","query":"main"},{"title":"","query":"dropped"},{"title":"Router","query":"mux.go"}]}` + "\n```"
	if got := ParsePages(out, 8); !reflect.DeepEqual(got, []Page{{"Overview", "main", -1}, {"Router", "mux.go", -1}}) {
		t.Fatalf("got %+v", got)
	}
	if lines := ParsePages(`{"pages":[{"title":"Setup\nsucceeded\r\n\tnext","query":"q"}]}`, 8); len(lines) != 1 || lines[0].Title != "Setup succeeded next" {
		t.Fatalf("a title over several lines: %+v", lines)
	}
	if ParsePages("no json here", 8) != nil {
		t.Fatal("prose without JSON produced a plan")
	}
	if got := ParsePages(`{"pages":[`+strings.Repeat(`{"title":"Page","query":"q"},`, 8)+`{"title":"One too many","query":"q"}]}`, 8); len(got) != 8 {
		t.Fatalf("%d pages planned, want at most 8", len(got))
	}
	long := ParsePages(fmt.Sprintf(`{"pages":[{"title":%q,"query":%q}]}`, strings.Repeat("標", 200), strings.Repeat("q", 600)), 8)
	if len(long) != 1 || len([]rune(long[0].Title)) != MaxTitle || len(long[0].Query) != MaxQuery {
		t.Fatalf("a long title and query: %+v", long)
	}
	overview, sections, ok := ParseHeads(`{"overview":{"title":" Overview ","query":"main"},"sections":[{"title":"Routing","query":"mux"},{"title":"","query":"x"},{"title":"Middleware"}]}`, 1)
	if !ok || overview != (Page{"Overview", "main", -1}) || !reflect.DeepEqual(sections, []Page{{"Routing", "mux", -1}}) {
		t.Fatalf("heads: %+v %+v %v", overview, sections, ok)
	}
	// An untitled section keeps its place, so the next stays with its own.
	if _, sections, _ := ParseHeads(`{"overview":{"title":"O"},"sections":[{"title":"","query":"x"},{"title":"Middleware"}]}`, 3); !reflect.DeepEqual(sections, []Page{{"", "x", -1}, {"Middleware", "", -1}}) {
		t.Fatalf("an untitled section: %+v", sections)
	}
	if _, _, ok := ParseHeads(`{"sections":[{"title":"Routing"}]}`, 9); ok {
		t.Fatal("an outline without an overview was taken")
	}
}
