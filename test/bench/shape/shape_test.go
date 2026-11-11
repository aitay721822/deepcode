package shape

import (
	"reflect"
	"testing"

	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/knowledge/wiki/store"
)

// TestOf: a page whose files weigh under a page's lines is thin, one over 20
// pages' crowded (D and E, at the bounds, neither), the overview and a page
// planned before files neither; a project a merged unit covers has a page,
// one no page covers has none; the code files on no page are unnamed, a
// manifest not being code.
func TestOf(t *testing.T) {
	paths := []string{"a/x.go", "b/y1.go", "b/y2.go", "c/z.go", "d/d.go", "e/e.go", "lib/go.mod", "lib/l.go", "svc/go.mod", "svc/main.go"}
	lines := map[string]int{"a/x.go": 50, "b/y1.go": 1500, "b/y2.go": 1500, "c/z.go": 10, "d/d.go": place.LinesPerPage, "e/e.go": CrowdedPages * place.LinesPerPage,
		"lib/l.go": 1, "svc/main.go": 300}
	pages := []store.Page{{Title: "Overview", Files: &store.PageFiles{}},
		{Title: "A", Files: &store.PageFiles{Chosen: []string{"a/x.go"}, Units: []string{"a/"}}},
		{Title: "B", Files: &store.PageFiles{Chosen: []string{"b/y1.go"}, Placed: []string{"b/y2.go", "lib/l.go"}, Units: []string{"b/, lib/"}}},
		{Title: "D", Files: &store.PageFiles{Chosen: []string{"d/d.go"}, Units: []string{"d/"}}}, {Title: "E", Files: &store.PageFiles{Chosen: []string{"e/e.go"}, Units: []string{"e/"}}},
		{Title: "Old"}}
	want := Shape{Pages: 6, Thin: 1, Crowded: 1, Homeless: []string{"svc/"}, Unnamed: []string{"c/z.go", "svc/main.go"}}
	if got := Of(pages, paths, lines); !reflect.DeepEqual(got, want) {
		t.Errorf("Of = %+v, want %+v", got, want)
	}
}
