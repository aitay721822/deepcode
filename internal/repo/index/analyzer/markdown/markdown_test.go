package markdown

import (
	"fmt"
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer/syntax"
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
