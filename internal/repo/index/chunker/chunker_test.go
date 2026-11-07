package chunker

import (
	"fmt"
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer"
)

func lines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %d", i+1)
	}
	return out
}

func summary(chunks []Chunk) string {
	var parts []string
	for _, c := range chunks {
		parts = append(parts, fmt.Sprintf("%d-%d %s", c.StartLine, c.EndLine, c.Symbol))
	}
	return strings.Join(parts, " | ")
}

func TestChunks(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		syms  []analyzer.Symbol
		want  string
	}{
		{"small neighbours merge into one chunk", lines(30),
			[]analyzer.Symbol{{Name: "a", StartLine: 3, EndLine: 10}, {Name: "b", StartLine: 12, EndLine: 20}},
			"1-30 a, b"},
		{"a symbol that fits stays whole", lines(200),
			[]analyzer.Symbol{{Name: "big", StartLine: 1, EndLine: 100}, {Name: "next", StartLine: 101, EndLine: 200}},
			"1-100 big | 101-200 next"},
		{"a large class splits along its methods", lines(300),
			[]analyzer.Symbol{{Name: "C", StartLine: 1, EndLine: 300, Children: []analyzer.Symbol{
				{Name: "m1", StartLine: 5, EndLine: 100},
				{Name: "m2", StartLine: 101, EndLine: 299},
			}}},
			"1-100 C, C.m1 | 101-220 C.m2 | 221-300 C.m2, C"},
		{"a namespace splits along its types, however small", lines(100),
			[]analyzer.Symbol{{Name: "N", Kind: "namespace", StartLine: 1, EndLine: 100, Children: []analyzer.Symbol{
				{Name: "A", StartLine: 3, EndLine: 50},
				{Name: "B", StartLine: 51, EndLine: 99},
			}}},
			"1-50 N, N.A | 51-100 N.B, N"},
		{"a Go method keeps its receiver", lines(10),
			[]analyzer.Symbol{{Name: "Get", Container: "Store", StartLine: 1, EndLine: 10}},
			"1-10 Store.Get"},
		{"plain text is cut into windows", lines(250), nil,
			"1-120  | 121-240  | 241-250 "},
	}
	for _, c := range cases {
		if got := summary(Chunks(c.lines, c.syms)); got != c.want {
			t.Errorf("%s:\ngot  %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestChunksCutAtBlankLines(t *testing.T) {
	l := lines(200)
	l[109] = "" // line 110 is blank, inside the last third of the first window
	got := summary(Chunks(l, []analyzer.Symbol{{Name: "f", StartLine: 1, EndLine: 200}}))
	if want := "1-110 f | 111-200 f"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestChunksSkipBlankRegions(t *testing.T) {
	l := []string{"", "", "func a() {}", "", ""}
	got := Chunks(l, []analyzer.Symbol{{Name: "a", StartLine: 3, EndLine: 3}})
	if len(got) != 1 || got[0].Content == "" {
		t.Fatalf("got %+v", got)
	}
	if got := Chunks([]string{"", "  "}, nil); len(got) != 0 {
		t.Fatalf("blank file produced chunks: %+v", got)
	}
}
