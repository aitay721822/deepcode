package aside

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/repo/index/analyzer"
)

// raw is each unit's Raw by its key, failing t on a key twice.
func raw(t *testing.T, units []place.Unit) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, u := range units {
		if _, ok := out[u.Key]; ok {
			t.Errorf("key %q twice", u.Key)
		}
		out[u.Key] = u.Raw
	}
	return out
}

// TestGroup: units at most are unchanged; past it the folder holding the
// most units directly (ties: the deeper, then the first by name) becomes one
// unit in the folder above, so nested guides merge bottom-up, until they are
// at most; a folder's own tests join its subfolders' under its key; a folder
// of one unit lifts it up, and merging ends at the root; a flat folder of 150
// guides is one unit of them all.
func TestGroup(t *testing.T) {
	docs := func(files ...string) []place.Unit { return place.AsideUnits(analyzer.Docs, files, nil, nil) }
	nested := docs("docs/a/1.md", "docs/a/2.md", "docs/a/3.md", "docs/b/1.md", "docs/b/2.md", "README.md")
	if got := Group(nested, 6); !reflect.DeepEqual(got, nested) {
		t.Errorf("at most: %+v", got)
	}
	for _, c := range []struct {
		units []place.Unit
		most  int
		want  map[string][]string
	}{
		{nested, 4, map[string][]string{"docs/a/": {"docs/a/1.md", "docs/a/2.md", "docs/a/3.md"}, "docs/b/1.md": {"docs/b/1.md"}, "docs/b/2.md": {"docs/b/2.md"}, "README.md": {"README.md"}}},
		{nested, 2, map[string][]string{"docs/": {"docs/a/1.md", "docs/a/2.md", "docs/a/3.md", "docs/b/1.md", "docs/b/2.md"}, "README.md": {"README.md"}}},
		{docs("a/1.md", "a/2.md", "b/1.md", "b/2.md"), 3, map[string][]string{"a/": {"a/1.md", "a/2.md"}, "b/1.md": {"b/1.md"}, "b/2.md": {"b/2.md"}}},
		{place.AsideUnits(analyzer.Tests, []string{"pkg/a_test.go", "pkg/sub/x_test.go", "pkg/sub2/y_test.go", "other/z_test.go"}, nil, nil), 2,
			map[string][]string{"pkg/ (tests)": {"pkg/ (tests)", "pkg/sub/ (tests)", "pkg/sub2/ (tests)"}, "other/ (tests)": {"other/ (tests)"}}},
		{place.AsideUnits(analyzer.Examples, []string{"examples/a/main.go", "examples/b/main.go", "examples/c/main.go", "x/examples/d/main.go"}, nil, nil), 2,
			map[string][]string{"examples/": {"examples/a/", "examples/b/", "examples/c/"}, "x/examples/d/": {"x/examples/d/"}}},
		{docs("x/y/1.md", "z/2.md"), 1, map[string][]string{"./": {"x/y/1.md", "z/2.md"}}},
	} {
		if got := raw(t, Group(c.units, c.most)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Group(%d units, %d) = %v, want %v", len(c.units), c.most, got, c.want)
		}
	}
	// The heaviest first, a merged unit's keys and files in path order, though its parts came by weight.
	lines := map[string]int{"README.md": 50, "docs/b/2.md": 30, "docs/b/1.md": 10, "docs/a/2.md": 7, "docs/a/3.md": 6, "docs/a/1.md": 5}
	weighed := Group(place.AsideUnits(analyzer.Docs, []string{"docs/a/1.md", "docs/a/2.md", "docs/a/3.md", "docs/b/1.md", "docs/b/2.md", "README.md"}, lines, nil), 4)
	var order []string
	for _, u := range weighed {
		order = append(order, u.Key)
	}
	if a := []string{"docs/a/1.md", "docs/a/2.md", "docs/a/3.md"}; !slices.Equal(order, []string{"README.md", "docs/b/2.md", "docs/a/", "docs/b/1.md"}) ||
		!slices.Equal(weighed[2].Raw, a) || !slices.Equal(weighed[2].Files, a) {
		t.Errorf("by weight: %v, %+v", order, weighed[2])
	}
	var flat []string
	for i := range 150 {
		flat = append(flat, fmt.Sprintf("docs/g%03d.md", i))
	}
	if g := Group(docs(flat...), 100); len(g) != 1 || g[0].Key != "docs/" || !slices.Equal(g[0].Raw, flat) || !slices.Equal(g[0].Files, flat) || g[0].Weight != 150 {
		t.Errorf("150 guides: %d units, %+v", len(g), g[0].Key)
	}
}
