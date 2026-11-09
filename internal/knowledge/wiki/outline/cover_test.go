package outline

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestUnnamed: a code file is named by its path, its name, its name without
// the extension as a whole word, or a small folder holding it; not by a
// sibling's path, a folder of more than folderFiles, or a word holding its
// name. Configuration and prose are no code to name.
func TestUnnamed(t *testing.T) {
	files := []string{"mw/profiler.go", "mw/get_head.go", "mw/nocache.go", "mw/realip.go", "mw/throttle.go", "a/b/x.cs", "a/b/y.cs", "go.mod", "README.md"}
	for i := range 9 {
		files = append(files, fmt.Sprintf("big/f%d.go", i))
	}
	pages := []Page{
		{"Head and caching", "mw/get_head.go nocache.go", -1},
		{"Real IP", "RealIP a/b/ big/", -1},
		{"Throttling", "throttled requests", -1},
	}
	got := Unnamed(pages, files)
	want := []string{"mw/profiler.go", "mw/throttle.go"}
	for i := range 9 {
		want = append(want, fmt.Sprintf("big/f%d.go", i))
	}
	if !slices.Equal(got, want) {
		t.Errorf("unnamed %v, want %v", got, want)
	}
}

// TestCover: the files no page names go to the page naming the path that
// shares most folders with them, a small folder's two or more as the folder,
// the largest first, while a query has room, in characters and in the names
// its search reads; the rest come back.
func TestCover(t *testing.T) {
	files := []string{"src/api/a.cs", "src/api/b.cs", "src/domain/order.cs", "src/domain/buyer.cs", "src/domain/events/paid.cs", "lib/util.go", "lib/huge.go"}
	lines := map[string]int{"src/api/a.cs": 10, "src/api/b.cs": 10, "src/domain/events/paid.cs": 50, "lib/util.go": 5, "lib/huge.go": 900}
	pages := []Page{
		{"API", "src/api/handlers.cs", -1},
		{"Domain", "src/domain/order.cs src/domain/buyer.cs", -1},
		{"Full", strings.Repeat("x", MaxQuery-5), -1},
	}
	// lib/ first, the largest, to the first page given fewest; paid.cs alone of
	// its folder, to the page sharing src/domain; src/api/ to the page sharing it.
	got, left := Cover(pages, files, lines)
	if got[0].Query != "src/api/handlers.cs lib/ src/api/" || got[1].Query != "src/domain/order.cs src/domain/buyer.cs src/domain/events/paid.cs" {
		t.Errorf("queries %q, %q", got[0].Query, got[1].Query)
	}
	if len(left) != 0 || Unnamed(got, files) != nil {
		t.Errorf("left %v, unnamed %v", left, Unnamed(got, files))
	}
	// No page with room: the file comes back, its page untouched.
	full := []Page{{"Full", strings.Repeat("x", MaxQuery-3), -1}}
	if got, left := Cover(full, []string{"lib/huge.go"}, nil); !slices.Equal(left, []string{"lib/huge.go"}) || got[0].Query != full[0].Query {
		t.Errorf("no room: left %v", left)
	}
	// Room counts characters, not bytes.
	wide := []Page{{"Wide", strings.Repeat("x", MaxQuery-8), -1}}
	if got, left := Cover(wide, []string{"路徑/a.go"}, nil); len(left) != 0 || !strings.HasSuffix(got[0].Query, " 路徑/a.go") {
		t.Errorf("wide: left %v", left)
	}
	// A query naming 16 paths: the page search reads no 17th, so the file goes
	// to another page, or comes back.
	sixteen := make([]string, 16)
	for i := range sixteen {
		sixteen[i] = fmt.Sprintf("lib/f%d.go", i)
	}
	many := strings.Join(sixteen, " ")
	if got, left := Cover([]Page{{"Files", many, -1}, {"Other", "src/x.go", -1}}, []string{"lib/huge.go"}, nil); len(left) != 0 || got[0].Query != many || got[1].Query != "src/x.go lib/huge.go" {
		t.Errorf("sixteen: %q, %q, left %v", got[0].Query, got[1].Query, left)
	}
	if _, left := Cover([]Page{{"Files", many, -1}}, []string{"lib/huge.go"}, nil); !slices.Equal(left, []string{"lib/huge.go"}) {
		t.Errorf("sixteen alone: left %v", left)
	}
	// A file of one project, its own project's page full, goes to no other project's page.
	project := []Page{{"Ordering", "src/ordering/" + strings.Repeat("x", MaxQuery), -1}, {"Catalog", "src/catalog/ai.cs", -1}}
	if got, left := Cover(project, []string{"src/ordering/events/paid.cs", "src/catalog/x.cs"}, nil); !slices.Equal(left, []string{"src/ordering/events/paid.cs"}) ||
		got[1].Query != "src/catalog/ai.cs src/catalog/x.cs" {
		t.Errorf("another project's page: %q, left %v", got[1].Query, left)
	}
}

// TestRecipes: the # and ## sections of a README that show code, theirs or
// a ### one's under them, by their titles up to a parenthesis or question;
// not the README's title, a section of prose, a ### one, nor a heading in code.
func TestRecipes(t *testing.T) {
	src := "# Lib\n```sh\nnpm i lib\n```\n## Install\n```sh\nnpm i lib\n```\n## Features\n- fast\n# Recipes\n## Transient updates (for often occurring state-changes)\n" +
		"text\n### Steps\n```js\nsubscribe()\n```\n## Why `lib`?\n~~~\n## in code\n~~~\n## License\nMIT\n"
	for _, s := range []string{src, strings.ReplaceAll(src, "\n", "\r\n")} {
		if got := Recipes(s); !slices.Equal(got, []string{"Install", "Transient updates", "Why lib"}) {
			t.Errorf("recipes %q", got)
		}
	}
}

// TestReadme: a recipe no page names goes in front of the query of the page
// naming most of its words, behind the README's path, at most perPage to a
// page, never to the overview or a section's own page; one with no room
// comes back.
func TestReadme(t *testing.T) {
	pages := []Page{{"Overview", "overview", -1}, {"Store", "src/vanilla.ts", -1}, {"Subscriptions", "subscribe updates listeners", 1},
		{"Middleware", "src/middleware.ts persist", 1}, {"Full", strings.Repeat("x", MaxQuery-10), -1}}
	// Persist middleware is named already; Async actions shares no word, so goes to the page given fewest.
	got, left := Readme(pages, "README.md", []string{"Transient updates", "Persist middleware", "Async actions", "Selecting state slices", "Reset state", "Immer"})
	want := []string{"overview", "src/vanilla.ts", "README.md Selecting state slices Transient updates subscribe updates listeners",
		"README.md Reset state Async actions src/middleware.ts persist", strings.Repeat("x", MaxQuery-10)}
	for i, w := range want {
		if got[i].Query != w {
			t.Errorf("page %d: %q, want %q", i, got[i].Query, w)
		}
	}
	if !slices.Equal(left, []string{"Immer"}) {
		t.Errorf("left %v", left)
	}
	// A query naming 16 paths would lose its 16th to README.md: the subject comes back.
	sixteen := make([]string, 16)
	for i := range sixteen {
		sixteen[i] = fmt.Sprintf("lib/f%d.go", i)
	}
	many := []Page{{"Overview", "o", -1}, {"Files", strings.Join(sixteen, " "), -1}}
	if got, left := Readme(many, "README.md", []string{"Caching"}); got[1].Query != strings.Join(sixteen, " ") || !slices.Equal(left, []string{"Caching"}) {
		t.Errorf("sixteen names: %q, left %v", got[1].Query, left)
	}
	// A query whose 16th term reaches one recipe keeps it in reach: the other comes back.
	reach := "aaa bbb ccc ddd eee fff ggg hhh iii jjj kkk lll mmm nnn zebra"
	if got, left := Readme([]Page{{"Overview", "o", -1}, {"Store", reach, -1}}, "README.md", []string{"zebra", "quartz"}); got[1].Query != reach || !slices.Equal(left, []string{"quartz"}) {
		t.Errorf("a recipe pushed out of reach: %q, left %v", got[1].Query, left)
	}
}

// TestShared: the folders a query's paths share with a unit, a file's name
// no folder.
func TestShared(t *testing.T) {
	for _, c := range []struct {
		text, unit string
		want       int
	}{
		{"src/a/x.go", "src/a/b/", 2}, {"src/a/", "src/a/y.go", 2}, {"src/a", "src/a/y.go", 1}, {"./src/x.go other", "src/y.go", 1}, {"words only", "src/y.go", 0},
	} {
		if got := shared(c.text, c.unit); got != c.want {
			t.Errorf("shared(%q, %q) = %d, want %d", c.text, c.unit, got, c.want)
		}
	}
}
