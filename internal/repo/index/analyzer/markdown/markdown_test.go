package markdown

import (
	"fmt"
	"strings"
	"testing"

	"deepcode/internal/repo/index/syntax"
)

func flat(syms []syntax.Symbol, depth int) []string {
	var out []string
	for _, s := range syms {
		out = append(out, fmt.Sprintf("%s%s %d-%d", strings.Repeat("  ", depth), s.Name, s.StartLine, s.EndLine))
		out = append(out, flat(s.Children, depth+1)...)
	}
	return out
}

// TestSections: each heading runs to the next of its level or above, its
// deeper ones inside; headings in code blocks, ones with no title or no space
// after the #, and setext ones are none; a closing run of # is no title.
func TestSections(t *testing.T) {
	src := "intro\n\n## First create a store\ntext\n```sh\n## a comment\n```\n" + // 1-7
		"# Recipes ##\n## Fetching everything\nx\n### Deeper\ny\n" + // 8-12
		"## Transient updates (for often occurring state-changes)\nz\n#hashtag\n#\n" + // 13-16
		"Setext\n======\n   ## C# usage\n~~~~\n# in tildes\n~~~~\nend\n" // 17-23
	got := flat(Sections([]byte(src)), 0)
	want := []string{
		"First create a store 3-7",
		"Recipes 8-23",
		"  Fetching everything 9-12",
		"    Deeper 11-12",
		"  Transient updates (for often occurring state-changes) 13-18",
		"  C# usage 19-23",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if s := Sections([]byte("## A\r\n\r\nb\r\n")); len(s) != 1 || s[0].EndLine != 3 || s[0].Signature != "## A" || s[0].Kind != "section" {
		t.Errorf("CRLF: %+v", s)
	}
	if s := Sections([]byte("## " + strings.Repeat("é", 200))); len([]rune(s[0].Name)) != maxTitle {
		t.Errorf("a long title keeps %d characters", len([]rune(s[0].Name)))
	}
	// Front matter's comments are no headings, and inline code is no fence.
	if s := flat(Sections([]byte("---\n# a comment\ntitle: x\n---\n```js```\n# Real\ntext\n")), 0); strings.Join(s, ";") != "Real 6-7" {
		t.Errorf("front matter and inline code: %v", s)
	}
	if s := flat(Sections([]byte("---\n# Not front matter\n")), 0); strings.Join(s, ";") != "Not front matter 2-2" {
		t.Errorf("an unclosed ---: %v", s)
	}
	if Sections(nil) != nil {
		t.Error("sections of an empty file")
	}
}

// TestIncludes: mdBook's include, rustdoc_include and playground name a file
// up to a space, colon or the braces, in code blocks too and each once; an
// escaped one, another directive, or a link is none.
func TestIncludes(t *testing.T) {
	src := "{{#include ../listings/a/src/main.rs:here}}\n```rust\n{{ #rustdoc_include ./b.rs:2:10 }}\n```\n" +
		"\\{{#include c.rs}}\n{{#playground d.rs editable}}\n{{#include ../listings/a/src/main.rs}}\n" +
		"{{#title x}}\n[e](e.rs)\n{{#include f/output.txt}}"
	want := []string{"../listings/a/src/main.rs", "./b.rs", "d.rs", "f/output.txt"}
	if got := Includes([]byte(src)); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %q, want %q", got, want)
	}
	if Includes([]byte("# Title\n")) != nil {
		t.Error("includes of a file with none")
	}
}
