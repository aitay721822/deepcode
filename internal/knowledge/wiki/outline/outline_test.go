package outline

import (
	"slices"
	"strings"
	"testing"
)

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

// TestTerms (G21): terms on one line each, clipped to MaxTerm characters,
// the empty dropped, at most MaxTerms; a page's query joins them.
func TestTerms(t *testing.T) {
	in := []string{" router\nmux ", "", strings.Repeat("長", MaxTerm+5)}
	for i := range MaxTerms + 1 {
		in = append(in, string(rune('a'+i)))
	}
	got := Terms(in)
	if len(got) != MaxTerms || got[0] != "router mux" || len([]rune(got[1])) != MaxTerm || got[MaxTerms-1] != "j" {
		t.Errorf("Terms = %q", got)
	}
	if q := (Page{Terms: got[:2]}).Query(); q != "router mux "+got[1] {
		t.Errorf("Query = %q", q)
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
