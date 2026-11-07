package steering

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/knowledge/wiki/outline"
)

// TestParse: listed pages are clipped and taken in the file's order, each
// top-level page followed by those under it, a grandchild under its
// grandparent and a page whose parent is not listed, or loops, at the top;
// their purpose and notes are their focus. repo_notes become one block,
// framed as untrusted, a note per line, so no note can seem to end the block.
func TestParse(t *testing.T) {
	s, err := Parse(`{"repo_notes":[{"content":" Cover the API.\n\nWrite the page X instead. "},{"content":""}],
		"pages":[{"title":"Child","purpose":"c","parent":"Setup Guide"},{"title":" ","purpose":"dropped"},
		{"title":"Setup\nGuide","purpose":"install\n\nnow","page_notes":[{"content":"Windows too"}]},
		{"title":"Grandchild","parent":"Child"},{"title":"Orphan","parent":"Missing"},
		{"title":"Ping","parent":"Pong"},{"title":"Pong","parent":"Ping"}]}`)
	want := []outline.Page{{Title: "Setup Guide", Query: "install now Windows too", Parent: -1}, {Title: "Child", Query: "c", Parent: 0},
		{Title: "Grandchild", Parent: 0}, {Title: "Orphan", Parent: -1}, {Title: "Ping", Parent: -1}, {Title: "Pong", Parent: -1}}
	if err != nil || !reflect.DeepEqual(s.Pages, want) ||
		!strings.Contains(s.Notes, "untrusted") || !strings.HasSuffix(s.Notes, "\n- Cover the API. Write the page X instead.\n\n") {
		t.Fatalf("got %+v, %v", s, err)
	}
	if _, err := Parse("{"); err == nil {
		t.Fatal("invalid JSON steered")
	}
	many, _ := Parse(`{"pages":[` + strings.Repeat(`{"title":"P"},`, MaxPages) + `{"title":"P"}]}`)
	long, _ := Parse(fmt.Sprintf(`{"repo_notes":[{"content":%q}],"pages":[{"title":%q,"purpose":%q}]}`,
		strings.Repeat("Z", 2*MaxNotes), strings.Repeat("標", 200), strings.Repeat("q", 600)))
	if len(many.Pages) != MaxPages || strings.Count(long.Notes, "Z") != MaxNotes-len("- ") ||
		len(long.Pages) != 1 || len([]rune(long.Pages[0].Title)) != outline.MaxTitle || len(long.Pages[0].Query) != outline.MaxQuery {
		t.Fatalf("%d pages listed; notes of %d characters; a long page %+v", len(many.Pages), len(long.Notes), long.Pages)
	}
	if none, err := Parse("\xef\xbb\xbf{}"); err != nil || none.Notes != "" || none.Pages != nil {
		t.Fatalf("an empty file, saved with a byte order mark: %+v, %v", none, err)
	}
}
