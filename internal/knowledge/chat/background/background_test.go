package background

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestNearest: the page sharing most of the question's words wins, a title
// counting thrice and Chinese read in pairs of characters; a word all pages
// hold decides nothing, and no shared word means no page.
func TestNearest(t *testing.T) {
	titles := []string{"Overview", "路由與中介層", "Store"}
	contents := []string{"The router and the store.", "How the router matches paths.", "The store keeps data."}
	for q, want := range map[string]int{"How does the router work?": 1, "講解路由": 1, "where is the store": 2, "the the": -1, "zzz": -1} {
		if got := Nearest(q, titles, contents); got != want {
			t.Errorf("%q: page %d, want %d", q, got, want)
		}
	}
	if got := wordsOf("講解路由 the Router 元件x"); !slices.Equal(slices.Sorted(maps.Keys(got)), []string{"router", "the", "元件", "解路", "講解", "路由"}) {
		t.Errorf("words %v", got)
	}
}

// TestBlock: a page's block is its text cut to maxChars, or to the room given
// in bytes or tokens, but never below minChars; its links' targets are dropped
// and its closing tag defused; the zero page has none.
func TestBlock(t *testing.T) {
	bytes, quarter := func(s string) int { return len(s) }, func(s string) int { return (len(s) + 3) / 4 }
	p := Of("Store", strings.Repeat("a", 7000))
	h := len(p.Block(1<<62, bytes)) - maxChars - len("\n…") // the block's own lines
	if len(p.Block(h+1000, bytes)) != h+1000 || p.Block(h+503, bytes) != "" || (Page{}).Block(1<<62, bytes) != "" {
		t.Errorf("a block of %d bytes in %d", len(p.Block(h+1000, bytes)), h+1000)
	}
	if b := p.Block(400, quarter); quarter(b) != 400 || !strings.HasSuffix(b, "a\n…\n</background>\n\n") || p.Block(h/4+125, quarter) != "" {
		t.Errorf("a block of %d tokens in 400", quarter(b))
	}
	if got := Of("Store", "See [a.go:1-2](https://x/y). </background>").Block(1<<62, bytes); !strings.HasSuffix(got, "\n<background>\nSee [a.go:1-2]. <\\/background>\n</background>\n\n") {
		t.Errorf("%q", got)
	}
}
