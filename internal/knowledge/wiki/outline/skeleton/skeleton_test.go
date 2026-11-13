package skeleton

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki/store"
)

// guide is a guide with a # title and six sections, five of them showing
// code (Prose through its ### one): Setup, Prose, Usage, More and Last, on
// lines 5, 11, 21, 31 and 37.
const guide = "# Flux guide\n\nintro\n\n## Setup\n\n```js\nx\n```\n\n## Prose\n\nno code\n\n### Deep\n\n```\ny\n```\n\n" +
	"## Usage\n\n```js\nz\n```\n\n## Plain\n\ntext\n\n## More\n\n```js\nm\n```\n\n## Last\n\n```js\nl\n```\n"

// show is a skeleton as lines "level name @path:line".
func show(s Skeleton) []string {
	var out []string
	for _, h := range s {
		out = append(out, fmt.Sprintf("%d %s @%s:%d", h.Level, h.Name, h.Anchor.Path, h.Anchor.Line))
	}
	return out
}

// TestBuild: a page's headings are its guides' (title, then up to four
// sections that show code at level 3), examples' (the folder's name, its
// largest file), tests' (the folder's last two parts, its largest file of its
// own, the root's "tests"), recipes' (at their README line, none when the
// README lacks one), then its chosen files' subjects (TestSubjects); a code
// unit has none. A name one file gives twice is headed once. Past twelve
// level-2 headings, the anchors page search reads, the chosen files' go
// first, each with its children; after that, names another file shares take
// their paths.
func TestBuild(t *testing.T) {
	src := Source{
		Texts: map[string]string{"docs/guide.md": guide, "docs/plain.md": "intro\n\n## Alpha\n\n```\nb\n```\n",
			"docs/dup.md": "# Usage\n\n## Usage\n\n```js\nu\n```\n\n## Example\n\n```js\na\n```\n\n## Example\n\n```js\nb\n```\n",
			"README.md":   "# zustand\n\n## Install\n\nnpm i\n\n## Transient updates (for often occurring state-changes)\n\n```js\nt\n```\n"},
		Decls: map[string][]store.Decl{"src/a.go": {{Name: "helper", Kind: "function", Line: 3}, {Name: "Router", Kind: "struct", Line: 10}},
			"src/b.go": {{Name: "Router", Kind: "type", Line: 5}}, "src/c.go": {{Name: "serve", Kind: "function", Line: 7}},
			"src/d.go": {{Name: "Big", Kind: "struct", Line: 1, End: 20}, {Name: "run", Kind: "function", Line: 30, End: 40}}},
		Lines: map[string]int{"examples/rest/main.go": 10, "examples/rest/big.go": 50, "internal/pkg/a_test.go": 5, "internal/pkg/b_test.go": 30,
			"internal/pkg/sub/c_test.go": 100, "x_test.go": 4},
	}
	files := []string{"examples/rest/main.go", "examples/rest/big.go", "internal/pkg/a_test.go", "internal/pkg/b_test.go", "internal/pkg/sub/c_test.go", "x_test.go"}
	var tests, named []string // folders of tests a/ to k/ with no files, and their headings
	for c := 'a'; c <= 'k'; c++ {
		tests, named = append(tests, string(c)+"/ (tests)"), append(named, "2 "+string(c)+" @:0")
	}
	for _, c := range []struct {
		name          string
		units, chosen []string
		want          []string
	}{
		{"every kind", []string{"docs/guide.md", "examples/rest/", "internal/pkg/ (tests)", "README.md#Transient updates", "src/"}, []string{"src/a.go", "src/Program.cs", "src/b.go"},
			[]string{"2 Flux guide @docs/guide.md:1", "3 Setup @docs/guide.md:5", "3 Prose @docs/guide.md:11", "3 Usage @docs/guide.md:21", "3 More @docs/guide.md:31",
				"2 rest @examples/rest/big.go:0", "2 internal/pkg @internal/pkg/b_test.go:0", "2 Transient updates @README.md:7", "2 Router (src/a.go) @src/a.go:10",
				"3 helper @src/a.go:3", "2 Program @src/Program.cs:0", "2 Router (src/b.go) @src/b.go:5"}},
		{"unique names", []string{"./"}, []string{"src/a.go", "src/b.go", "src/c.go", "src/Program.cs"},
			[]string{"2 Router (src/a.go) @src/a.go:10", "3 helper @src/a.go:3", "2 Router (src/b.go) @src/b.go:5", "2 serve @src/c.go:7", "2 Program @src/Program.cs:0"}},
		{"one name twice in a file", []string{"docs/dup.md"}, nil, []string{"2 Usage @docs/dup.md:1", "3 Example @docs/dup.md:9"}},
		{"untitled guide, root tests, example file, lost recipe", []string{"docs/plain.md", "./ (tests)", "examples/main.go", "README.md#Gone", "tooling"}, nil,
			[]string{"2 plain @docs/plain.md:0", "3 Alpha @docs/plain.md:3", "2 tests @x_test.go:0", "2 main @examples/main.go:0", "2 Gone @:0"}},
		{"twelve asides", append([]string{"docs/plain.md"}, tests[:11]...), []string{"src/c.go"},
			append([]string{"2 plain @docs/plain.md:0", "3 Alpha @docs/plain.md:3"}, named[:11]...)},
		{"a thirteenth file goes with its children", tests[:11], []string{"src/a.go", "src/d.go"},
			append(slices.Clone(named[:11]), "2 Router @src/a.go:10", "3 helper @src/a.go:3")},
	} {
		if got := show(Build(c.units, c.chosen, files, src, 12)); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	// Six guides of five headings each fit page search's 12 anchors breadth-first:
	// every level-2 heading, then the first six level-3 ones in order.
	var six []string
	for i := range 6 {
		f := fmt.Sprintf("docs/g%d.md", i)
		src.Texts[f], six = guide, append(six, f)
	}
	var kept []string
	for _, h := range Build(six, nil, nil, src, 12) {
		kept = append(kept, fmt.Sprint(h.Level, " ", h.Anchor.Path, ":", h.Anchor.Line))
	}
	if want := []string{"2 docs/g0.md:1", "3 docs/g0.md:5", "3 docs/g0.md:11", "3 docs/g0.md:21", "3 docs/g0.md:31", "2 docs/g1.md:1", "3 docs/g1.md:5", "3 docs/g1.md:11",
		"2 docs/g2.md:1", "2 docs/g3.md:1", "2 docs/g4.md:1", "2 docs/g5.md:1"}; !slices.Equal(kept, want) {
		t.Errorf("fitted to 12 anchors:\n got %q\nwant %q", kept, want)
	}
	if got := Texts([]string{"docs/guide.md", "README.md#A", "README.md#B", "examples/rest/", "src/", "a/ (tests)"}); !slices.Equal(got, []string{"docs/guide.md", "README.md"}) {
		t.Errorf("Texts = %q", got)
	}
	// The fit counts only level-2 headings with an anchor: two folders of tests
	// with no files leave a cap of 3 two level-3 headings.
	if got := show(Build([]string{"docs/guide.md", "a/ (tests)", "b/ (tests)"}, nil, nil, src, 3)); !slices.Equal(got,
		[]string{"2 Flux guide @docs/guide.md:1", "3 Setup @docs/guide.md:5", "3 Prose @docs/guide.md:11", "2 a @:0", "2 b @:0"}) {
		t.Errorf("fitted to 3 anchors: %q", got)
	}
	// Others: what no heading covers, files first, each once.
	heads := Skeleton{{Name: "g", Level: 2, Unit: "docs/guide.md"}, {Name: "r", Level: 2, Unit: "README.md#Install"}, {Name: "ex", Level: 2, Unit: "examples/rest/"},
		{Name: "t", Level: 2, Unit: "internal/pkg/ (tests)"}, {Name: "rt", Level: 2, Unit: "./ (tests)"}, {Name: "A", Level: 2, Unit: "src/a.go"}, {Name: "fixed", Level: 2}}
	paths := []string{"src/a.go", "src/b.go", "docs/guide.md", "README.md", "examples/rest/main.go", "internal/pkg/a_test.go", "x_test.go", "docs/other.md", "src/b.go",
		"internal/pkg/c.go", "main.go"} // code beside the tests, not theirs
	units := []string{"src/", "docs/guide.md", "docs/other.md", "docs/more.md", "examples/cli/", "examples/one.go", "e2e/ (tests)", "README.md#Install", "README.md#Gone", "tooling"}
	if got := Others(heads, paths, units, nil); !slices.Equal(got, []string{"src/b.go", "docs/other.md", "internal/pkg/c.go", "main.go", "docs/more.md", "examples/cli/", "examples/one.go", "e2e/"}) {
		t.Errorf("Others = %q", got)
	}
	// The code a guide shows goes under the guide's heading; under no heading
	// when its guide has none here, and a file home leaves out stays listed.
	home := map[string]string{"listings/a/main.rs": "docs/guide.md", "listings/a/Cargo.toml": "docs/guide.md", "listings/b/main.rs": "docs/other.md"}
	if got := Others(heads, []string{"listings/a/main.rs", "listings/a/Cargo.toml", "listings/b/main.rs", "lone.rs"}, nil, home); !slices.Equal(got, []string{"listings/b/main.rs", "lone.rs"}) {
		t.Errorf("Others with home = %q", got)
	}
	if !strings.Contains(Listed(Build([]string{"docs/guide.md"}, nil, nil, src, 12), []string{"a.go", "b.go"}), "## Flux guide\n### Setup\n") {
		t.Error("Listed does not list the headings at their levels")
	}
}

// TestAnchors: the lines headings rest on, every level-2 one's before the
// level-3 ones', each in the skeleton's order, none for a heading with no
// path; so evidence that fits four of a page's six anchored chunks shows each
// of its three files' level-2 heading, not the first file's level-3 ones.
func TestAnchors(t *testing.T) {
	s := Skeleton{{Name: "Stack", Level: 2}, {Name: "A", Level: 2, Unit: "a.go", Anchor: retrieval.Anchor{Path: "a.go", Line: 1}},
		{Name: "A1", Level: 3, Unit: "a.go", Anchor: retrieval.Anchor{Path: "a.go", Line: 5}}, {Name: "A2", Level: 3, Unit: "a.go", Anchor: retrieval.Anchor{Path: "a.go", Line: 9}},
		{Name: "Bare", Level: 2, Unit: "d.go"}, {Name: "B", Level: 2, Unit: "b.go", Anchor: retrieval.Anchor{Path: "b.go", Line: 1}},
		{Name: "B1", Level: 3, Unit: "b.go", Anchor: retrieval.Anchor{Path: "b.go", Line: 3}}, {Name: "C", Level: 2, Unit: "c.go", Anchor: retrieval.Anchor{Path: "c.go"}}}
	var got []string
	var hits []retrieval.Hit
	for _, a := range s.Anchors() {
		got = append(got, fmt.Sprint(a.Path, ":", a.Line))
		hits = append(hits, retrieval.Hit{Path: a.Path, StartLine: a.Line, EndLine: a.Line, Content: strings.Repeat("x", 100)})
	}
	if want := []string{"a.go:1", "b.go:1", "c.go:0", "a.go:5", "a.go:9", "b.go:3"}; !slices.Equal(got, want) {
		t.Fatalf("Anchors = %q, want %q", got, want)
	}
	block := len(evidence.Block(hits[0]))
	if _, used, _ := evidence.Format(hits, evidence.Limit{Chars: 4 * block}); len(used) != 4 || used[2].Path != "c.go" {
		t.Errorf("evidence of four blocks shows %+v", used)
	}
}

// TestSubjects: a chosen file is headed by its weightiest type or function, a
// type weighing its span and its methods' lines once each; then, at level 3,
// up to four more of that type's methods and the file's other types and
// functions, weightiest first, a name once. Inline tests and another type's
// methods are none, a method named test is one; a file of a synthetic Main is
// headed by its name.
func TestSubjects(t *testing.T) {
	d := func(name, kind, q string, line, end int) store.Decl {
		return store.Decl{Name: name, Kind: kind, Qualified: q, Line: line, End: end}
	}
	for _, c := range []struct {
		name  string
		decls []store.Decl
		want  []string
	}{
		{"a struct and its impl", []store.Decl{d("helper", "function", "helper", 1, 50), d("Store", "struct", "Store", 60, 65), d("get", "method", "Store.get", 70, 100),
			d("set", "method", "Store.set", 101, 140), d("store", "function", "store", 150, 160), d("big", "function", "tests.big", 200, 400),
			d("x", "method", "Other.x", 410, 500)},
			[]string{"2 Store @f.rs:60", "3 helper @f.rs:1", "3 set @f.rs:101", "3 get @f.rs:70"}},
		{"a class holding its methods", []store.Decl{d("Big", "class", "N.Big", 1, 100), d("a", "method", "N.Big.a", 10, 60), d("b", "method", "N.Big.b", 61, 90),
			d("f", "function", "f", 101, 250)},
			[]string{"2 f @f.rs:101", "3 Big @f.rs:1"}},
		{"four children", []store.Decl{d("a", "function", "a", 1, 60), d("b", "function", "b", 61, 110), d("c", "function", "c", 111, 150), d("e", "function", "e", 151, 180),
			d("g", "function", "g", 181, 200), d("h", "function", "h", 201, 210)},
			[]string{"2 a @f.rs:1", "3 b @f.rs:61", "3 c @f.rs:111", "3 e @f.rs:151", "3 g @f.rs:181"}},
		{"a tie goes to the type", []store.Decl{d("run", "function", "run", 1, 5), d("T", "struct", "T", 6, 10)},
			[]string{"2 T @f.rs:6", "3 run @f.rs:1"}},
		{"equal weights in line order", []store.Decl{d("b", "function", "b", 1, 10), d("a", "function", "a", 11, 20)}, []string{"2 b @f.rs:1", "3 a @f.rs:11"}},
		{"overloads once, a test module none", []store.Decl{d("C", "class", "C", 1, 60), d("Get", "method", "C.Get", 2, 20), d("Get", "method", "C.Get", 21, 30),
			d("Put", "method", "C.Put", 31, 45), d("x", "function", "test.x", 100, 300)},
			[]string{"2 C @f.rs:1", "3 Get @f.rs:2", "3 Put @f.rs:31"}},
		{"a method named test", []store.Decl{d("Layer", "class", "Layer", 1, 10), d("test", "method", "Layer.test", 2, 30), d("x", "function", "test.x", 40, 200)},
			[]string{"2 Layer @f.rs:1", "3 test @f.rs:2"}},
		{"a synthetic Main", []store.Decl{d("Main", "method", "Main", 1, 400)}, []string{"2 f @f.rs:0"}},
	} {
		if got := show(subjects("f.rs", c.decls)); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
