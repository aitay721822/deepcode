package place

import (
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer"
	graphstore "deepcode/internal/repo/index/graph/store"
)

// keys are units' keys.
func keys(units []Unit) []string {
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = u.Key
	}
	return out
}

// TestUnits (G7): a file belongs to the nearest folder above it but the root
// holding a project manifest (no Directory.Build.props), else its folder; a
// unit of only configuration files is tooling; asides by kind, a long root
// README a guide; thin units merge by imports, then by kin counted below the
// folders every unit shares.
func TestUnits(t *testing.T) {
	paths := []string{
		"go.mod", "main.go", "lib/p.go",
		"src/A/A.csproj", "src/A/x.cs", "src/A/sub/y.cs",
		"src/Directory.Build.props", "src/B/z.cs",
		"docs/guide.md", "README.md", "a/x_test.go", ".github/ci.yml",
	}
	lines := map[string]int{"main.go": 300, "lib/p.go": 300, "src/A/x.cs": 300, "src/A/sub/y.cs": 300, "src/B/z.cs": 300, "README.md": 50}
	code, asides := Units(paths, lines, nil, nil)
	if got, want := keys(code), []string{"./", "lib/", "src/A/", "src/B/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("code units %v, want %v", got, want)
	}
	if code[2].Kind != Project || code[3].Kind != Dir || !reflect.DeepEqual(code[2].Files, []string{"src/A/A.csproj", "src/A/sub/y.cs", "src/A/x.cs"}) {
		t.Errorf("src/A/: %+v; src/B/: %+v", code[2], code[3])
	}
	if code[0].Kind != Dir || !reflect.DeepEqual(code[0].Files, []string{"go.mod", "main.go"}) {
		t.Errorf("a root manifest makes no project: %+v", code[0])
	}
	if !reflect.DeepEqual(code[2].Code, []string{"src/A/sub/y.cs", "src/A/x.cs"}) {
		t.Errorf("src/A/'s choices %v", code[2].Code)
	}
	want := map[string][]string{
		analyzer.Docs: {"docs/guide.md"}, analyzer.Tests: {"a/x_test.go"},
		analyzer.Tooling: {".github/ci.yml", "README.md", "src/Directory.Build.props"},
	}
	if len(asides) != 3 {
		t.Fatalf("asides %+v", asides)
	}
	for _, a := range asides {
		if !reflect.DeepEqual(a.Files, want[a.Kind]) {
			t.Errorf("%s: %v, want %v", a.Kind, a.Files, want[a.Kind])
		}
	}
	lines["README.md"] = 200
	if _, asides := Units(paths, lines, nil, nil); asides[0].Kind != analyzer.Docs || len(asides[0].Files) != 2 {
		t.Errorf("a long README is no guide: %+v", asides[0])
	}

	// petclinic's resources: thin, no imports, kin below src/main/ only; model/ joins what imports it, of two as kin.
	paths = []string{"src/main/java/app/owner/Owner.java", "src/main/java/app/vet/Vet.java",
		"src/main/resources/db/h2/data.sql", "src/main/resources/db/mysql/data.sql", "src/main/java/app/model/Base.java"}
	lines = map[string]int{"src/main/java/app/owner/Owner.java": 400, "src/main/java/app/vet/Vet.java": 400,
		"src/main/resources/db/h2/data.sql": 40, "src/main/resources/db/mysql/data.sql": 60, "src/main/java/app/model/Base.java": 50}
	edges := []graphstore.Edge{{Src: "src/main/java/app/vet/Vet.java", Dst: "src/main/java/app/model/Base.java"}}
	code, _ = Units(paths, lines, nil, edges)
	if got, want := keys(code), []string{"src/main/java/app/owner/", "src/main/java/app/model/, src/main/java/app/vet/",
		"src/main/resources/db/h2/, src/main/resources/db/mysql/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("merged %v, want %v", got, want)
	}
}

// TestCaps (G5): a unit's cap is min(the distinct names its files declare,
// unclipped, 1 + its weight / LinesPerPage), a configuration file weighing a
// quarter, at least 1.
func TestCaps(t *testing.T) {
	names := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = string(rune('A' + i))
		}
		return out
	}
	paths := []string{"a/a.go", "a/x.json", "b/b.go", "c/data.sql"}
	lines := map[string]int{"a/a.go": 450, "a/x.json": 400, "b/b.go": 900, "c/data.sql": 900}
	declared := map[string][]string{"a/a.go": names(8), "b/b.go": names(7)}
	code, _ := Units(paths, lines, declared, nil)
	got := map[string]int{}
	for _, u := range code {
		got[u.Key] = u.Cap
	}
	if want := map[string]int{"a/": 6, "b/": 7, "c/": 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("caps %v, want %v", got, want)
	}
}

// unit is a code unit for the shape tests.
func unit(key string, cap int, weight float64, files ...string) Unit {
	return Unit{Key: key, Kind: Dir, Files: files, Code: files, Cap: cap, Weight: weight, Raw: []string{key}}
}

// TestSections (G7b): cap ≥ 2 starts a section; cap-1 units join by imports
// before kin, over passes; leftovers group among themselves, and one alone
// is a top-level page; heaviest first.
func TestSections(t *testing.T) {
	units := []Unit{
		unit("pkg/a/", 3, 400, "pkg/a/a.go"), unit("svc/b/", 2, 300, "svc/b/b.go"),
		unit("pkg/c/", 1, 50, "pkg/c/c.go"), unit("pkg/a/d/", 1, 40, "pkg/a/d/d.go"),
		unit("x/e/", 1, 30, "x/e/e.go"), unit("y/f/", 1, 20, "y/f/f.go"),
		unit("z/g/", 1, 10, "z/g/g.go"), unit("w/h/", 1, 5, "w/h/h.go"),
	}
	edges := []graphstore.Edge{{Src: "pkg/c/c.go", Dst: "svc/b/b.go"}, {Src: "x/e/e.go", Dst: "y/f/f.go"}, {Src: "w/h/h.go", Dst: "pkg/c/c.go"}}
	want := [][]int{{0, 3}, {1, 2, 7}, {4, 5}, {6}}
	if got := Sections(units, edges); !reflect.DeepEqual(got, want) {
		t.Errorf("Sections = %v, want %v", got, want)
	}
}

// TestShares (G7c): docs and examples by their own units and weight, a third
// of it, whatever p; tests and tooling by p too.
func TestShares(t *testing.T) {
	lines := map[string]int{}
	var docs, tests []string
	for i := range 43 {
		f := "docs/g" + string(rune('a'+i%26)) + strings.Repeat("x", i/26) + ".md"
		docs, lines[f] = append(docs, f), 100
	}
	for _, f := range []string{"t1/a_test.go", "t2/b_test.go", "t3/c_test.go"} {
		tests, lines[f] = append(tests, f), 300
	}
	lines["Makefile"] = 500
	asides := []Unit{
		{Key: analyzer.Docs, Kind: analyzer.Docs, Files: docs, Weight: Weigh(docs, lines, false)},
		{Key: analyzer.Tests, Kind: analyzer.Tests, Files: tests, Weight: Weigh(tests, lines, false)},
		{Key: analyzer.Tooling, Kind: analyzer.Tooling, Files: []string{"Makefile"}, Weight: 500},
	}
	for _, c := range []struct{ p, docs, tests, tooling int }{{2, 15, 1, 1}, {40, 15, 3, 4}} {
		Shares(asides, lines, c.p)
		if asides[0].Cap != c.docs || asides[1].Cap != c.tests || asides[2].Cap != c.tooling {
			t.Errorf("p=%d: %d %d %d, want %d %d %d", c.p, asides[0].Cap, asides[1].Cap, asides[2].Cap, c.docs, c.tests, c.tooling)
		}
	}
	guides := []Unit{{Key: analyzer.Docs, Kind: analyzer.Docs, Files: []string{"docs/a.md", "docs/b.md"}, Weight: 30 * LinesPerPage}} // weight enough for 11
	if Shares(guides, nil, 2); guides[0].Cap != 2 {
		t.Errorf("two heavy guides: %d pages", guides[0].Cap)
	}
}

// TestFlat (G7d): flat iff 1 + code caps + asides' caps ≤ FlatPages.
func TestFlat(t *testing.T) {
	units := []Unit{unit("a/", 3, 300), unit("b/", 2, 200)}
	asides := []Unit{
		{Key: analyzer.Tests, Kind: analyzer.Tests, Files: []string{"a/x_test.go"}, Weight: 100},
		{Key: analyzer.Tooling, Kind: analyzer.Tooling, Files: []string{"Makefile"}, Weight: 100},
	}
	if !Flat(units, asides, nil) {
		t.Error("8 pages is not flat")
	}
	if Flat(units, append(asides, Unit{Key: analyzer.Docs, Kind: analyzer.Docs, Files: []string{"g.md"}, Cap: 1}), nil) {
		t.Error("9 pages is flat")
	}
}

// TestCeiling (G6): the most pages per weight lose one first, docs at a third
// of their weight among them, heads counted, never below 1; then the lightest
// merge, into what it imports with, else the lightest; a plan within the
// ceiling is untouched.
func TestCeiling(t *testing.T) {
	two := func() []Unit { return []Unit{unit("x/", 5, 500, "x/x.go"), unit("y/", 6, 3000, "y/y.go")} }
	units, _, wanted := Ceiling(two(), nil, nil, nil, 13)
	if wanted != 14 || units[0].Cap != 4 || units[1].Cap != 6 {
		t.Errorf("trim: wanted %d, caps %d %d", wanted, units[0].Cap, units[1].Cap)
	}
	tests := []Unit{{Key: analyzer.Tests, Kind: analyzer.Tests, Files: []string{"a/x_test.go", "b/y_test.go"}, Weight: 1000, Cap: 5}}
	if units, _, _ := Ceiling(two(), tests, nil, nil, 100); units[0].Cap != 5 || units[1].Cap != 6 || tests[0].Cap != 2 { // tests' share of 11 code pages
		t.Errorf("untouched: caps %d %d, tests %d", units[0].Cap, units[1].Cap, tests[0].Cap)
	}
	if units, _, _ := Ceiling(two(), tests, nil, nil, 12); units[0].Cap != 2 || tests[0].Cap != 1 { // 16 wanted: x trims to 2, the tests' share of 8 code pages 1
		t.Errorf("trimmed with tests: x %d, tests %d", units[0].Cap, tests[0].Cap)
	}
	docs := []Unit{{Key: analyzer.Docs, Kind: analyzer.Docs, Files: []string{"a.md", "b.md", "c.md", "d.md"}, Cap: 4, Weight: 600}} // 4 pages for 200 weighed: more per weight than x
	units, _, _ = Ceiling(two(), docs, nil, nil, 17)
	if docs[0].Cap != 3 || units[0].Cap != 5 {
		t.Errorf("docs first: docs %d, x %d", docs[0].Cap, units[0].Cap)
	}
	four := []Unit{unit("a/", 1, 40, "a/a.go"), unit("b/", 1, 30, "b/b.go"), unit("c/", 1, 20, "c/c.go"), unit("d/", 1, 10, "d/d.go")}
	units, sections, wanted := Ceiling(four, nil, []graphstore.Edge{{Src: "d/d.go", Dst: "a/a.go"}}, nil, 3) // d/ imports a/: one section of two, with its head
	if wanted != 6 || !reflect.DeepEqual(keys(units), []string{"a/, d/", "b/, c/"}) || Wanted(units, sections, nil, nil) != 3 {
		t.Errorf("merge: wanted %d, units %v", wanted, keys(units))
	}
	for _, u := range units {
		if u.Cap < 1 {
			t.Errorf("%s cap %d", u.Key, u.Cap)
		}
	}
}
