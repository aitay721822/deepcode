package skeleton

import (
	"fmt"
	"slices"
	"strings"
	"testing"

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
// README lacks one), then its chosen files' main declarations (a type before
// a function, else the file's name); a code unit has none. A name one file
// gives twice is headed once. Past six level-2 headings, the chosen files' go
// first, each with its children; after that, names another file shares take
// their paths.
func TestBuild(t *testing.T) {
	src := Source{
		Texts: map[string]string{"docs/guide.md": guide, "docs/plain.md": "intro\n\n## Alpha\n\n```\nb\n```\n",
			"docs/dup.md": "# Usage\n\n## Usage\n\n```js\nu\n```\n\n## Example\n\n```js\na\n```\n\n## Example\n\n```js\nb\n```\n",
			"README.md":   "# zustand\n\n## Install\n\nnpm i\n\n## Transient updates (for often occurring state-changes)\n\n```js\nt\n```\n"},
		Decls: map[string][]store.Decl{"src/a.go": {{Name: "helper", Kind: "function", Line: 3}, {Name: "Router", Kind: "struct", Line: 10}},
			"src/b.go": {{Name: "Router", Kind: "type", Line: 5}}, "src/c.go": {{Name: "serve", Kind: "function", Line: 7}}},
		Lines: map[string]int{"examples/rest/main.go": 10, "examples/rest/big.go": 50, "internal/pkg/a_test.go": 5, "internal/pkg/b_test.go": 30,
			"internal/pkg/sub/c_test.go": 100, "x_test.go": 4},
	}
	files := []string{"examples/rest/main.go", "examples/rest/big.go", "internal/pkg/a_test.go", "internal/pkg/b_test.go", "internal/pkg/sub/c_test.go", "x_test.go"}
	for _, c := range []struct {
		name          string
		units, chosen []string
		want          []string
	}{
		{"every kind", []string{"docs/guide.md", "examples/rest/", "internal/pkg/ (tests)", "README.md#Transient updates", "src/"}, []string{"src/a.go", "src/Program.cs", "src/b.go"},
			[]string{"2 Flux guide @docs/guide.md:1", "3 Setup @docs/guide.md:5", "3 Prose @docs/guide.md:11", "3 Usage @docs/guide.md:21", "3 More @docs/guide.md:31",
				"2 rest @examples/rest/big.go:0", "2 internal/pkg @internal/pkg/b_test.go:0", "2 Transient updates @README.md:7", "2 Router @src/a.go:10", "2 Program @src/Program.cs:0"}},
		{"unique names", []string{"./"}, []string{"src/a.go", "src/b.go", "src/c.go", "src/Program.cs"},
			[]string{"2 Router (src/a.go) @src/a.go:10", "2 Router (src/b.go) @src/b.go:5", "2 serve @src/c.go:7", "2 Program @src/Program.cs:0"}},
		{"one name twice in a file", []string{"docs/dup.md"}, nil, []string{"2 Usage @docs/dup.md:1", "3 Example @docs/dup.md:9"}},
		{"untitled guide, root tests, example file, lost recipe", []string{"docs/plain.md", "./ (tests)", "examples/main.go", "README.md#Gone", "tooling"}, nil,
			[]string{"2 plain @docs/plain.md:0", "3 Alpha @docs/plain.md:3", "2 tests @x_test.go:0", "2 main @examples/main.go:0", "2 Gone @:0"}},
		{"seven asides", []string{"docs/plain.md", "a/ (tests)", "b/ (tests)", "c/ (tests)", "d/ (tests)", "e/ (tests)", "f/ (tests)"}, []string{"src/c.go"},
			[]string{"2 plain @docs/plain.md:0", "3 Alpha @docs/plain.md:3", "2 a @:0", "2 b @:0", "2 c @:0", "2 d @:0", "2 e @:0"}},
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
