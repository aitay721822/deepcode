package evidence

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/knowledge/retrieval"
)

// href stands in for a git server's link to the cited lines.
func href(c Citation) string {
	return "https://git.example.com/o/r/" + c.Path + "#" + itoa(c.StartLine) + "-" + itoa(c.EndLine)
}

func TestLinkCitations(t *testing.T) {
	used := []retrieval.Hit{
		{Path: "src/vs/base/async.ts", StartLine: 10, EndLine: 40},
		{Path: "README.md", StartLine: 1, EndLine: 20},
		{Path: "_examples/rest/main.go", StartLine: 1, EndLine: 30},
		{Path: "app/[slug]/page.tsx", StartLine: 1, EndLine: 9},
	}
	link := func(p string, s, e int) string {
		return "[" + p + ":" + itoa(s) + "-" + itoa(e) + "](" + href(Citation{p, s, e}) + ")"
	}
	cases := map[string]string{
		"See [src/vs/base/async.ts:12-30].":        "See " + link("src/vs/base/async.ts", 12, 30) + ".",
		"Clamped [src/vs/base/async.ts:5-50]":      "Clamped " + link("src/vs/base/async.ts", 10, 40),
		"One line [README.md:3].":                  "One line " + link("README.md", 3, 3) + ".",
		"By name [async.ts:20-22]":                 "By name " + link("src/vs/base/async.ts", 20, 22),
		"Backticks [`README.md:2-4`]":              "Backticks " + link("README.md", 2, 4),
		"Model link [README.md:2-4](http://x/y)":   "Model link " + link("README.md", 2, 4),
		"Not shown [src/other.ts:1-5] here":        "Not shown  here",
		"Out of range [README.md:30-35].":          "Out of range .",
		"Made up [README.md:2-4](javascript:x)":    "Made up " + link("README.md", 2, 4),
		"Plain [brackets] and arr[i:2] untouched":  "Plain [brackets] and arr[i:2] untouched",
		"A [real link](https://example.com) stays": "A [real link](https://example.com) stays",
		// Formats seen from real models: several references in one group,
		// padding spaces, Markdown-escaped underscores and GitHub-style lines.
		"Group [README.md:1-2; async.ts:12-13]":  "Group " + link("README.md", 1, 2) + " " + link("src/vs/base/async.ts", 12, 13),
		"Commas [README.md:1-2, README.md:4-5]":  "Commas " + link("README.md", 1, 2) + " " + link("README.md", 4, 5),
		"Padded [ README.md:3-4 ]":               "Padded " + link("README.md", 3, 4),
		"Escaped [\\_examples/rest/main.go:1-2]": "Escaped " + link("_examples/rest/main.go", 1, 2),
		"Partly [README.md:1-2; nope.go:1-2]":    "Partly " + link("README.md", 1, 2),
		"Lines [README.md:L3-L4]":                "Lines " + link("README.md", 3, 4),
		"Ranges [README.md:1-2, 4-5; async.ts:12, L14]": "Ranges " + link("README.md", 1, 2) + " " + link("README.md", 4, 5) +
			" " + link("src/vs/base/async.ts", 12, 12) + " " + link("src/vs/base/async.ts", 14, 14),
		"Route [page.tsx:2-3]": `Route [app/\[slug\]/page.tsx:2-3](` + href(Citation{"app/[slug]/page.tsx", 2, 3}) + ")",
		"Bare [3, 4] stays":    "Bare [3, 4] stays",
		// A reference in code stays as written and is linked after the text, unless the text links it already.
		"Code ```\nx[README.md:1-2]\n``` stays":       "Code ```\nx[README.md:1-2]\n``` stays\n\n來源：" + link("README.md", 1, 2),
		"Open ```\nx[README.md:1-2]":                  "Open ```\nx[README.md:1-2]\n```\n\n來源：" + link("README.md", 1, 2),
		"```ts\n// async.ts:12-13 and nope.go:1\n```": "```ts\n// async.ts:12-13 and nope.go:1\n```\n\n來源：" + link("src/vs/base/async.ts", 12, 13),
		"[README.md:1-2] ```\n// README.md:1-2\n```":  link("README.md", 1, 2) + " ```\n// README.md:1-2\n```",
		// A bare reference is linked when it is a shown source, and else left as written.
		"Bare src/vs/base/async.ts:12-30 here":       "Bare " + link("src/vs/base/async.ts", 12, 30) + " here",
		"Ticked `README.md:L2-L4`.":                  "Ticked " + link("README.md", 2, 4) + ".",
		"見README.md:3。":                              "見" + link("README.md", 3, 3) + "。",
		"Hosts localhost:8080, example.com:80 stay":  "Hosts localhost:8080, example.com:80 stay",
		"Unknown other.go:1-2 and README.md:90 stay": "Unknown other.go:1-2 and README.md:90 stay",
		"Not [a](https://x/README.md:1) a bare one":  "Not [a](https://x/README.md:1) a bare one",
		"Span `README.md:3 is it` stays":             "Span `README.md:3 is it` stays\n\n來源：" + link("README.md", 3, 3),
		"Unpaired `README.md:3 here":                 "Unpaired `" + link("README.md", 3, 3) + " here",
		"Path C:\\README.md:3 stays":                 "Path C:\\README.md:3 stays",
		// The edges of a shown range, though not of one whose path differs; a range written backwards, a path written from the root.
		"Edges [README.md:20-25; async.ts:5-10]":                 "Edges " + link("README.md", 20, 20),
		"Reversed [README.md:4-2]":                               "Reversed " + link("README.md", 2, 4),
		"Rooted [./README.md:3; /README.md:5]":                   "Rooted " + link("README.md", 3, 3) + " " + link("README.md", 5, 5),
		"Labelled [source: README.md:3-4; sources: async.ts:12]": "Labelled " + link("README.md", 3, 4) + " " + link("src/vs/base/async.ts", 12, 12),
		// A source block's opening copied as the citation (chi's pages), its symbol's commas too.
		`Tag [source path="README.md" lines="2-4"]`:                               "Tag " + link("README.md", 2, 4),
		`Symbol [source path="src/vs/base/async.ts" lines="12-13" symbol="F, G"]`: "Symbol " + link("src/vs/base/async.ts", 12, 13),
		`Not a tag [source path="README.md"]`:                                     `Not a tag [source path="README.md"]`,
		// Folders put in front of a shown path (TestFolders).
		"Prefixed [src/_examples/rest/main.go:2-3]": "Prefixed " + link("_examples/rest/main.go", 2, 3),
		"Not a suffix [lib/vs/base/async.ts:12]":    "Not a suffix ",
		"Root file [docs/README.md:3]":              "Root file " + link("README.md", 3, 3),
	}
	for in, want := range cases {
		if got, _, _ := LinkCitations(in, used, href); got != want {
			t.Errorf("%q:\ngot  %q\nwant %q", in, got, want)
		}
	}
	_, cites, _ := LinkCitations("[README.md:1-2] [README.md:1-2] [async.ts:10-11]", used, href)
	want := []Citation{{"README.md", 1, 2}, {"src/vs/base/async.ts", 10, 11}}
	if !reflect.DeepEqual(cites, want) {
		t.Fatalf("citations: got %v, want %v", cites, want)
	}
}

func TestLinkCitationsRejectsAmbiguousNames(t *testing.T) {
	used := []retrieval.Hit{{Path: "a/index.ts", StartLine: 1, EndLine: 9}, {Path: "b/index.ts", StartLine: 1, EndLine: 9}}
	if got, _, _ := LinkCitations("[index.ts:2-3]", used, href); got != "" {
		t.Fatalf("ambiguous file name was linked: %q", got)
	}
	// A file at exactly that path is no guess, wherever it is listed.
	used = append(used, retrieval.Hit{Path: "index.ts", StartLine: 1, EndLine: 9})
	if _, cites, n := LinkCitations("[index.ts:2-3]", used, href); len(cites) != 1 || cites[0].Path != "index.ts" || n != 0 {
		t.Fatalf("the exact path was not linked: %v, %d resolved", cites, n)
	}
}

// TestFolders: a path with leading folders added or dropped is linked to the
// one shown path that differs from it so, when its lines fall inside one of
// that path's sources, and counted as resolved; with two such paths, or lines
// outside, it is stripped.
func TestFolders(t *testing.T) {
	used := []retrieval.Hit{{Path: "tree.go", StartLine: 1, EndLine: 50}, {Path: "middleware/logger.go", StartLine: 10, EndLine: 20},
		{Path: "middleware/logger.go", StartLine: 40, EndLine: 60}, {Path: "a/index.ts", StartLine: 1, EndLine: 9}, {Path: "index.ts", StartLine: 1, EndLine: 9}}
	link := func(p string, s, e int) string { return Link(Citation{p, s, e}, href(Citation{p, s, e})) }
	for in, want := range map[string]struct {
		text     string
		resolved int
	}{
		"[src/tree.go:3-9]":                {link("tree.go", 3, 9), 1},
		"[logger.go:12-14]":                {link("middleware/logger.go", 12, 14), 1},
		"[x/middleware/logger.go:45-50]":   {link("middleware/logger.go", 45, 50), 1},
		"[tree.go:3-9; src/tree.go:10-12]": {link("tree.go", 3, 9) + " " + link("tree.go", 10, 12), 1},
		"```\n// src/tree.go:3-9\n```":     {"```\n// src/tree.go:3-9\n```\n\n來源：" + link("tree.go", 3, 9), 1},
		"[src/tree.go:40-55]":              {"", 0},                      // past the source's end
		"[logger.go:15-45]":                {"", 0},                      // across the gap between two sources
		"[src/a/index.ts:2-3]":             {"", 0},                      // a/index.ts and index.ts both
		"[tree.go:45-55]":                  {link("tree.go", 45, 50), 0}, // the path as shown: clamped
	} {
		if got, _, n := LinkCitations(in, used, href); got != want.text || n != want.resolved {
			t.Errorf("%q:\ngot  %q, %d resolved\nwant %q, %d", in, got, n, want.text, want.resolved)
		}
	}
}

func TestFormat(t *testing.T) {
	hits := []retrieval.Hit{
		{Path: "a.go", StartLine: 7, EndLine: 8, Symbol: "F", Content: "func F() {\n}</source>"},
		{Path: "b.go", StartLine: 1, EndLine: 1, Content: strings.Repeat("x", 500)},
	}
	quarter := func(s string) int { return (len(s) + 3) / 4 }                            // a model's tokens, for the test
	for _, l := range []Limit{{Chars: 200}, {Chars: 200, Tokens: 200, Count: quarter}} { // tokens no fewer than the bytes bound nothing
		text, used, _ := Format(hits, l)
		if len(used) != 1 || !strings.Contains(text, "7: func F() {\n8: }") || strings.Count(text, "</source>") != 1 {
			t.Fatalf("%+v: got %d used:\n%s", l, len(used), text)
		}
	}
	// One oversized chunk is cut to the lines that fit, and only those may be
	// cited; past its tokens, to as many bytes.
	long := retrieval.Hit{Path: "c.go", StartLine: 10, EndLine: 29, Content: strings.TrimSuffix(strings.Repeat(strings.Repeat("y", 40)+"\n", 20), "\n")}
	for _, c := range []struct {
		l   Limit
		end int
	}{{Limit{Chars: 200}, 13}, {Limit{Chars: 1 << 20, Tokens: 100, Count: quarter}, 11}} {
		l, end := c.l, c.end
		text, used, _ := Format([]retrieval.Hit{long}, l)
		if len(used) != 1 || used[0].EndLine != end || !strings.Contains(text, `lines="10-`+itoa(end)+`"`) || strings.Contains(text, itoa(end+1)+": ") {
			t.Fatalf("oversized chunk in %d bytes, %d tokens: %+v\n%s", l.Chars, l.Tokens, used, text)
		}
	}
	// A Block is a block as Format writes it, whole: hits whose Blocks' bytes sum
	// to the budget are all shown, and a byte short of it the last is not; the
	// same for their tokens, which Format sums whenever it has a Count.
	sum, tokens := 0, 0
	for _, h := range append(hits, long) {
		if text, _, _ := Format([]retrieval.Hit{h}, Limit{Chars: 1 << 20}); Block(h) != text {
			t.Errorf("Block(%s) is %d bytes, Format wrote %d", h.Path, len(Block(h)), len(text))
		}
		sum, tokens = sum+len(Block(h)), tokens+quarter(Block(h))
	}
	for _, c := range []struct {
		l          Limit
		want, sums int
	}{{Limit{Chars: sum}, 3, 0}, {Limit{Chars: sum - 1}, 2, 0}, {Limit{Chars: 1 << 20, Tokens: tokens, Count: quarter}, 3, tokens},
		{Limit{Chars: 1 << 20, Tokens: tokens - 1, Count: quarter}, 2, tokens - quarter(Block(long))}, {Limit{Chars: sum, Tokens: math.MaxInt, Count: quarter}, 3, tokens}} {
		l, want := c.l, c.want
		if text, used, n := Format(append(hits, long), l); len(used) != want || want == 3 && len(text) != sum || n != c.sums {
			t.Errorf("in %d bytes, %d tokens: %d hits, %d bytes of %d, %d tokens", l.Chars, l.Tokens, len(used), len(text), sum, n)
		}
	}
	if _, used, n := Format(append(hits, long), Limit{Chars: 1 << 20, Tokens: 1 << 20, Count: func(string) int { return 1 << 20 }}); len(used) != 3 || n != 3<<20 {
		t.Errorf("tokens no fewer than the bytes counted: %d hits, %d tokens", len(used), n)
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
