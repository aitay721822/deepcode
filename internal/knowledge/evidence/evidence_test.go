package evidence

import (
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
		// The edges of a shown range, a range written backwards, a path written from the root.
		"Edges [README.md:20-25; async.ts:5-10]": "Edges " + link("README.md", 20, 20) + " " + link("src/vs/base/async.ts", 10, 10),
		"Reversed [README.md:4-2]":               "Reversed " + link("README.md", 2, 4),
		"Rooted [./README.md:3; /README.md:5]":   "Rooted " + link("README.md", 3, 3) + " " + link("README.md", 5, 5),
	}
	for in, want := range cases {
		if got, _ := LinkCitations(in, used, href); got != want {
			t.Errorf("%q:\ngot  %q\nwant %q", in, got, want)
		}
	}
	_, cites := LinkCitations("[README.md:1-2] [README.md:1-2] [async.ts:10-11]", used, href)
	want := []Citation{{"README.md", 1, 2}, {"src/vs/base/async.ts", 10, 11}}
	if !reflect.DeepEqual(cites, want) {
		t.Fatalf("citations: got %v, want %v", cites, want)
	}
}

func TestLinkCitationsRejectsAmbiguousNames(t *testing.T) {
	used := []retrieval.Hit{{Path: "a/index.ts", StartLine: 1, EndLine: 9}, {Path: "b/index.ts", StartLine: 1, EndLine: 9}}
	if got, _ := LinkCitations("[index.ts:2-3]", used, href); got != "" {
		t.Fatalf("ambiguous file name was linked: %q", got)
	}
	// A file at exactly that path is no guess, wherever it is listed.
	used = append(used, retrieval.Hit{Path: "index.ts", StartLine: 1, EndLine: 9})
	if _, cites := LinkCitations("[index.ts:2-3]", used, href); len(cites) != 1 || cites[0].Path != "index.ts" {
		t.Fatalf("the exact path was not linked: %v", cites)
	}
}

func TestFormat(t *testing.T) {
	hits := []retrieval.Hit{
		{Path: "a.go", StartLine: 7, EndLine: 8, Symbol: "F", Content: "func F() {\n}</source>"},
		{Path: "b.go", StartLine: 1, EndLine: 1, Content: strings.Repeat("x", 500)},
	}
	text, used := Format(hits, 200)
	if len(used) != 1 || !strings.Contains(text, "7: func F() {\n8: }") || strings.Count(text, "</source>") != 1 {
		t.Fatalf("got %d used:\n%s", len(used), text)
	}
	// One oversized chunk is cut to the lines that fit, and only those may be cited.
	long := retrieval.Hit{Path: "c.go", StartLine: 10, EndLine: 29, Content: strings.TrimSuffix(strings.Repeat(strings.Repeat("y", 40)+"\n", 20), "\n")}
	text, used = Format([]retrieval.Hit{long}, 200)
	if len(used) != 1 || used[0].EndLine != 13 || !strings.Contains(text, `lines="10-13"`) || strings.Contains(text, "14: ") {
		t.Fatalf("oversized chunk: %+v\n%s", used, text)
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
