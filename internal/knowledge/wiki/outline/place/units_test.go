package place

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer"
	graphstore "deepcode/internal/repo/index/graphstore"
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

	// petclinic's resources: thin, no imports, kin below src/main/ only; model/ joins what imports it, of two as kin;
	// system/, at 150 weighed lines, is not thin (LinesPerPage), though under capLines: it stays a unit of its own.
	paths = []string{"src/main/java/app/owner/Owner.java", "src/main/java/app/vet/Vet.java", "src/main/java/app/system/Cache.java",
		"src/main/resources/db/h2/data.sql", "src/main/resources/db/mysql/data.sql", "src/main/java/app/model/Base.java"}
	lines = map[string]int{"src/main/java/app/owner/Owner.java": 400, "src/main/java/app/vet/Vet.java": 400, "src/main/java/app/system/Cache.java": 150,
		"src/main/resources/db/h2/data.sql": 40, "src/main/resources/db/mysql/data.sql": 60, "src/main/java/app/model/Base.java": 50}
	edges := []graphstore.Edge{{Src: "src/main/java/app/vet/Vet.java", Dst: "src/main/java/app/model/Base.java"}}
	code, _ = Units(paths, lines, nil, edges)
	if got, want := keys(code), []string{"src/main/java/app/owner/", "src/main/java/app/system/", "src/main/java/app/model/, src/main/java/app/vet/",
		"src/main/resources/db/h2/, src/main/resources/db/mysql/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("merged %v, want %v", got, want)
	}
}

// TestIllustrations: a code unit a guide includes a file of joins the docs,
// with the guide including the most of its files (the first by path of a
// tie), when it is thin or half its code's weight is included, and nothing
// but guides and other such units imports it, dropped until none does.
func TestIllustrations(t *testing.T) {
	paths := []string{"src/ch01.md", "src/ch02.md", "src/ch03.md", "README.md",
		"listings/a/Cargo.toml", "listings/a/output.txt", "listings/a/src/main.rs", "listings/b/Cargo.toml", "listings/b/src/lib.rs", "listings/b/src/main.rs",
		"app/Cargo.toml", "app/src/config.rs", "app/src/main.rs", "lib/Cargo.toml", "lib/src/lib.rs", "lib/tests/it.rs",
		"c/lib.rs", "d/main.rs", "e/main.rs", "f/lib.rs", "g/lib.rs", "h/main.rs", "half/lib.rs", "half/main.rs", "edge/main.rs", "edge/x.rs", "thin/main.rs", "thin/out.txt",
		"docs/gen/main.go", "k/lib.rs", "m/lib.rs"}
	lines := map[string]int{"listings/a/src/main.rs": 20, "listings/b/src/lib.rs": 300, "listings/b/src/main.rs": 100,
		"app/src/main.rs": 60, "app/src/config.rs": 1940, "lib/src/lib.rs": 50, "c/lib.rs": 30, "d/main.rs": 30, "e/main.rs": 300,
		"f/lib.rs": 30, "g/lib.rs": 30, "h/main.rs": 30, "half/lib.rs": 100, "half/main.rs": 100, "edge/main.rs": 60, "edge/x.rs": 40,
		"thin/main.rs": 30, "src/ch01.md": 2000, "src/ch02.md": 2000, "src/ch03.md": 2000}
	edges := []graphstore.Edge{
		{Src: "src/ch02.md", Dst: "listings/a/src/main.rs"}, {Src: "src/ch01.md", Dst: "listings/a/output.txt"}, // a tie: ch01
		{Src: "src/ch02.md", Dst: "listings/b/src/lib.rs"}, {Src: "src/ch03.md", Dst: "listings/b/src/lib.rs"}, {Src: "src/ch03.md", Dst: "listings/b/src/main.rs"},
		{Src: "listings/b/src/main.rs", Dst: "listings/b/src/lib.rs"},
		{Src: "src/ch01.md", Dst: "app/src/main.rs"},                                                 // a program quoted: 60 of 2000 lines
		{Src: "src/ch02.md", Dst: "lib/src/lib.rs"}, {Src: "lib/tests/it.rs", Dst: "lib/src/lib.rs"}, // a library its tests use
		{Src: "src/ch03.md", Dst: "c/lib.rs"}, {Src: "src/ch03.md", Dst: "d/main.rs"}, {Src: "d/main.rs", Dst: "c/lib.rs"}, // shown by another shown
		{Src: "f/lib.rs", Dst: "g/lib.rs"}, {Src: "src/ch01.md", Dst: "f/lib.rs"}, {Src: "src/ch01.md", Dst: "g/lib.rs"},
		{Src: "e/main.rs", Dst: "f/lib.rs"},  // e is code: f stays, then g, which f imports
		{Src: "README.md", Dst: "h/main.rs"}, // a README is no guide
		{Src: "src/ch02.md", Dst: "half/lib.rs"}, {Src: "src/ch02.md", Dst: "edge/x.rs"},
		{Src: "src/ch02.md", Dst: "thin/out.txt"},                                         // thin: shown, though none of its code is included
		{Src: "docs/gen/main.go", Dst: "k/lib.rs"},                                        // code under docs/ is no guide: it shows nothing,
		{Src: "src/ch01.md", Dst: "m/lib.rs"}, {Src: "docs/gen/main.go", Dst: "m/lib.rs"}, // and it guards what it imports
	}
	raw, rawAsides := Raw(paths, lines, nil)
	code, asides := Illustrations(raw, rawAsides, edges, lines)
	if got, want := keys(code), []string{"app/", "e/", "edge/", "f/", "g/", "h/", "k/", "lib/", "m/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("code %v, want %v", got, want)
	}
	docs := asides[slices.IndexFunc(asides, func(a Unit) bool { return a.Kind == analyzer.Docs })]
	want := map[string]string{"listings/a/Cargo.toml": "src/ch01.md", "listings/a/output.txt": "src/ch01.md", "listings/a/src/main.rs": "src/ch01.md",
		"listings/b/Cargo.toml": "src/ch03.md", "listings/b/src/lib.rs": "src/ch03.md", "listings/b/src/main.rs": "src/ch03.md",
		"c/lib.rs": "src/ch03.md", "d/main.rs": "src/ch03.md", "half/lib.rs": "src/ch02.md", "half/main.rs": "src/ch02.md",
		"thin/main.rs": "src/ch02.md", "thin/out.txt": "src/ch02.md"}
	if !reflect.DeepEqual(docs.Home, want) || len(docs.Files) != 4+len(want) || !slices.IsSorted(docs.Files) || docs.Weight != Weigh(docs.Files, lines, false) {
		t.Errorf("docs %+v", docs)
	}
	got := map[string][]string{}
	for _, u := range AsideUnits(docs.Kind, docs.Files, lines, docs.Home) {
		got[u.Key] = u.Files
	}
	if len(got) != 4 || len(got["src/ch01.md"]) != 4 || len(got["src/ch02.md"]) != 5 || len(got["src/ch03.md"]) != 6 {
		t.Errorf("guides %v", got)
	}
	// Its share counts guides, and Carry finds the shown code's guide.
	if n := Share(docs, lines, 0); n != 4 {
		t.Errorf("the docs' share %d, want 4 (one a guide)", n)
	}
	if o := Owners(raw, asides, lines)["listings/a/src/main.rs"]; o != (Owner{Key: "src/ch01.md", Kind: analyzer.Docs}) {
		t.Errorf("owner %+v", o)
	}
	if c, _ := Illustrations(raw, rawAsides[1:], edges, lines); len(c) != len(raw) {
		t.Error("no docs aside, yet code moved")
	}
	if rawAsides[0].Home != nil || len(rawAsides[0].Files) != 4 {
		t.Errorf("the docs aside passed in changed: %+v", rawAsides[0])
	}
}

// TestCaps (G5): a unit's cap is min(the distinct names its files declare,
// unclipped, 1 + its weight / capLines), a configuration file weighing a
// quarter, at least 1: a/ by its weight (550), b/ by its names (7, its weight
// allowing 8).
func TestCaps(t *testing.T) {
	names := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = string(rune('A' + i))
		}
		return out
	}
	paths := []string{"a/a.go", "a/x.json", "b/b.go", "c/data.sql"}
	lines := map[string]int{"a/a.go": 450, "a/x.json": 400, "b/b.go": 1500, "c/data.sql": 900}
	declared := map[string][]string{"a/a.go": names(8), "b/b.go": names(7)}
	code, _ := Units(paths, lines, declared, nil)
	got := map[string]int{}
	for _, u := range code {
		got[u.Key] = u.Cap
	}
	if want := map[string]int{"a/": 3, "b/": 7, "c/": 1}; !reflect.DeepEqual(got, want) {
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
	for _, c := range []struct{ p, docs, tests, tooling int }{{2, 8, 1, 1}, {40, 8, 3, 3}} { // docs 1 + (4300/3)/capLines; tooling by p, then its weight
		Shares(asides, lines, c.p)
		if asides[0].Cap != c.docs || asides[1].Cap != c.tests || asides[2].Cap != c.tooling {
			t.Errorf("p=%d: %d %d %d, want %d %d %d", c.p, asides[0].Cap, asides[1].Cap, asides[2].Cap, c.docs, c.tests, c.tooling)
		}
	}
	guides := []Unit{{Key: analyzer.Docs, Kind: analyzer.Docs, Files: []string{"docs/a.md", "docs/b.md"}, Weight: 30 * LinesPerPage}} // weight enough for 6
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
	// A flat plan keeps every page at the top: an aside of two pages gets no
	// head, so its caps alone count.
	if !Flat(units, []Unit{{Key: analyzer.Docs, Kind: analyzer.Docs, Cap: 2}}, nil) {
		t.Error("a two-page aside's head is no page of a flat plan")
	}
}

// TestWantedHeads (G7d): an aside of two pages or more counts its head, as
// the plan gives a part of two pages or more one, and one of one page none.
func TestWantedHeads(t *testing.T) {
	units := []Unit{unit("a/", 2, 200)} // one section of two, with its head
	docs := []Unit{{Key: analyzer.Docs, Kind: analyzer.Docs, Cap: 2, Weight: 200}}
	if got := Wanted(units, [][]int{{0}}, docs, nil); got != 7 { // 1 overview + 2 + head + 2 + head
		t.Errorf("two-page aside: %d pages wanted, want 7", got)
	}
	docs[0].Cap = 1
	if got := Wanted(units, [][]int{{0}}, docs, nil); got != 5 { // 1 + 2 + head + 1
		t.Errorf("one-page aside: %d pages wanted, want 5", got)
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
	if docs[0].Cap != 2 || units[0].Cap != 5 { // 19 wanted: docs' head takes one too, so docs trims to 2
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
