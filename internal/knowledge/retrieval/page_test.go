package retrieval

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestSearchPageReaches: after the anchors' chunks comes the code each anchor
// reaches, each anchor's next in turn, outside limit, then the search hits in
// what limit leaves the anchors; a chunk reached twice, or an anchor's own,
// costs no search hit; an anchor past limit, on no chunk, or sharing another's
// chunk reaches as it is entered; a header joined to a declaration frees a
// place the next ranked hit takes.
func TestSearchPageReaches(t *testing.T) {
	chunks := []string{
		"a.go|A|1|5|type A struct{}", "a.go|A.m|10|15|func (A) m() {}",
		"b.go|B|1|10|func B() {", "b.go||11|20|alpha beta alpha beta alpha beta }", // the top search hit, reached
		"c.go|C|1|5|type C struct{}", "c.go|C.x|10|12|func (C) x() {}", "c.go|C.y|20|22|func (C) y() {}",
		"d.go|D|1|10|func D() {", "d.go||11|20|d1", "d.go||21|30|d2", "d.go||31|40|}",
		"e.go|E|1|5|type E struct{}", "e.go|E.m|10|15|func (E) m() {}", "e.go|G|20|25|func G() { m() }", "e.go|H|30|35|func H() { G() }",
		"p.go|P|1|6|func P() {\n\tp1()\n}\nfunc Q() {\n\tq1()\n}", "p.go|p1|10|12|func p1() {}", "p.go|q1|14|16|func q1() {}",
		"h.go||1|3|package h", "h.go|D|4|10|func D() {}", "h.go|K|12|15|func K() { D() }",
		"j.go||1|3|package j", "j.go|J|4|9|type J struct{}",
		"s1.go||1|9|alpha beta", "s2.go||1|9|alpha", "s3.go||1|9|alpha",
	}
	syms := []string{"a.go A A struct 1 5", "a.go m A.m method 10 15", "b.go B B function 1 20", "c.go C C struct 1 5",
		"c.go x C.x method 10 12", "c.go y C.y method 20 22", "d.go D D function 1 40", "e.go E E struct 1 5", "e.go m E.m method 10 15",
		"e.go G G function 20 25", "e.go H H function 30 35", "p.go P P function 1 3", "p.go Q Q function 4 6", "p.go p1 p1 function 10 12",
		"p.go q1 q1 function 14 16", "h.go D D function 4 10", "h.go K K function 12 15", "j.go J J struct 1 9"}
	var many []Anchor
	for i := range 12 {
		chunks = append(chunks, fmt.Sprintf("n%02d.go||1|9|x", i))
		many = append(many, Anchor{Path: fmt.Sprintf("n%02d.go", i)})
	}
	r := retriever(seeded(t, inserts(chunks, syms)...))
	for _, c := range []struct {
		name    string
		anchors []Anchor
		limit   int
		want    []string
	}{
		{"a member, a declaration's rest, then one search hit, the top one reached already", []Anchor{{"a.go", 1}, {"b.go", 1}}, 3,
			[]string{"a.go:1-5", "b.go:1-10", "a.go:10-15", "b.go:11-20", "s1.go:1-9"}},
		{"in turn, a list run out skipped", []Anchor{{"c.go", 1}, {"d.go", 1}}, 2,
			[]string{"c.go:1-5", "d.go:1-10", "c.go:10-12", "d.go:11-20", "c.go:20-22", "d.go:21-30", "d.go:31-40"}},
		{"reached twice, and an anchor's own chunk reached, take no search hit", []Anchor{{"e.go", 1}, {"e.go", 20}, {"e.go", 30}}, 4,
			[]string{"e.go:1-5", "e.go:20-25", "e.go:30-35", "e.go:10-15", "b.go:11-20"}},
		{"an anchor past limit reaches nothing", append(slices.Clone(many), Anchor{"a.go", 1}), 12,
			[]string{"n00.go:1-9", "n01.go:1-9", "n02.go:1-9", "n03.go:1-9", "n04.go:1-9", "n05.go:1-9", "n06.go:1-9", "n07.go:1-9",
				"n08.go:1-9", "n09.go:1-9", "n10.go:1-9", "n11.go:1-9"}},
		{"an anchor on no chunk, then one entered, then one past limit", []Anchor{{"b.go", 999}, {"c.go", 1}, {"a.go", 1}}, 1,
			[]string{"c.go:1-5", "c.go:10-12", "c.go:20-22"}},
		{"two anchors sharing a chunk both reach", []Anchor{{"p.go", 1}, {"p.go", 4}}, 1,
			[]string{"p.go:1-6", "p.go:10-12", "p.go:14-16"}},
		{"a header joins a reached declaration, the place it frees to the next ranked", []Anchor{{"h.go", 0}, {"h.go", 12}}, 3,
			[]string{"h.go:1-10", "h.go:12-15", "b.go:11-20", "s1.go:1-9"}},
		// Synthetic: the chunker names such a header after its declaration (joinNames); this is Read's join.
		{"a reached header joins the anchor's chunk in its place", []Anchor{{"j.go", 5}}, 2,
			[]string{"j.go:1-9", "b.go:11-20", "s1.go:1-9"}},
	} {
		res, err := r.SearchPage(context.Background(), 1, "alpha beta", nil, c.anchors, c.limit)
		var got []string
		for _, h := range res.Hits {
			got = append(got, fmt.Sprintf("%s:%d-%d", h.Path, h.StartLine, h.EndLine))
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// TestSearchPageReachesImports: a reached chunk the best hits' imports also
// find leaves its place among them to the next imported one.
func TestSearchPageReachesImports(t *testing.T) {
	chunks := []string{"i.go|I|1|5|type I struct{}", "i.go|I.m|10|15|route route route route route route",
		"n.go||1|9|route a b c d e f g h i j k l m n o p", "n.go||10|19|route q r s t u v w x y z aa bb cc dd ee ff"}
	var edges []string
	for i := range 26 { // more than ftsLimit, so that n.go's chunks are found only as imported code
		chunks = append(chunks, fmt.Sprintf("f%02d.go||1|9|route route route route", i))
		edges = append(edges, fmt.Sprintf("(1, 'f%02d.go', 'i.go'), (1, 'f%02d.go', 'n.go')", i, i))
	}
	ins := append(inserts(chunks, []string{"i.go I I struct 1 5", "i.go m I.m method 10 15"}),
		`INSERT INTO file_edges(snapshot_id, src, dst) VALUES `+strings.Join(edges, ", "))
	res, err := retriever(seeded(t, ins...)).SearchPage(context.Background(), 1, "route", nil, []Anchor{{"i.go", 1}}, 4)
	var got []string
	for _, h := range res.Hits {
		got = append(got, fmt.Sprintf("%s:%d", h.Path, h.StartLine))
	}
	if err != nil || len(got) != 5 || got[0] != "i.go:1" || got[1] != "i.go:10" || !strings.HasPrefix(got[2], "f") ||
		!slices.Equal(slices.Sorted(slices.Values(got[3:])), []string{"n.go:1", "n.go:10"}) || res.Graph != 2 {
		t.Errorf("got %q (%d imported), %v; want i.go's two, a top hit, then n.go's two", got, res.Graph, err)
	}
}

// inserts writes chunks ("path|symbol|start|end|content") and symbols ("path
// name qualified kind start end") of snapshot 1.
func inserts(chunks, syms []string) []string {
	var c, y []string
	for i, s := range chunks {
		f := strings.SplitN(s, "|", 5)
		c = append(c, fmt.Sprintf("(1, '%s', '%s', %s, %s, '%s', 'h%d')", f[0], f[1], f[2], f[3], f[4], i))
	}
	for _, s := range syms {
		f := strings.Fields(s)
		y = append(y, fmt.Sprintf("(1, '%s', '%s', '%s', '%s', %s, %s)", f[0], f[1], f[2], f[3], f[4], f[5]))
	}
	return []string{`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES ` + strings.Join(c, ", "),
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES ` + strings.Join(y, ", ")}
}
