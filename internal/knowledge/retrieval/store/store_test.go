package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/platform/storage"
)

// TestRead: a header ranked with its declaration is read as one hit in the
// place of the first, and the places joins free go to the chunks ranked after,
// up to limit, the places before unchanged; a declaration ranked first takes
// its header in its own place; a header with no declaration after it, and an
// id no chunk has, are read as they are; per file, a file's later hits, joined
// or not, give their places to the files ranked after, yet a header still
// joins the declaration holding its file's place.
func TestRead(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// A.java and B.java: a header (lines 1-5, 1-4) and the declaration after it,
	// A.java a second declaration (21-40); F.java: a header alone, then a gap and
	// a declaration (5-9); C, D and G: a declaration from line 1, C.java a second
	// one (11-20).
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
		`INSERT INTO chunks(id, snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES
			(1, 1, 'A.java', '', 1, 5, 'import a;', 'h1'), (2, 1, 'A.java', 'A', 6, 20, 'class A {}', 'h2'),
			(3, 1, 'B.java', '', 1, 4, 'import b;', 'h3'), (4, 1, 'B.java', 'B', 5, 30, 'class B {}', 'h4'),
			(5, 1, 'C.java', 'C', 1, 10, 'class C {}', 'h5'), (6, 1, 'D.java', 'D', 1, 10, 'class D {}', 'h6'),
			(7, 1, 'C.java', 'C2', 11, 20, 'class C2 {}', 'h7'), (8, 1, 'A.java', 'A2', 21, 40, 'class A2 {}', 'h8'),
			(9, 1, 'F.java', '', 1, 3, 'import f;', 'h9'), (10, 1, 'G.java', 'G', 1, 10, 'class G {}', 'h10'),
			(11, 1, 'F.java', 'F2', 5, 9, 'class F2 {}', 'h11')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{DB: db}
	read := func(ranked []int64, limit int, perFile bool) []string {
		t.Helper()
		hits, err := s.Read(ctx, 1, ranked, limit, perFile)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range hits {
			out = append(out, fmt.Sprintf("%d %s:%d-%d %s", h.ChunkID, h.Path, h.StartLine, h.EndLine, h.Symbol))
		}
		return out
	}
	both, per, flat := []bool{false, true}, []bool{true}, []bool{false}
	for _, c := range []struct {
		name    string
		ranked  []int64
		limit   int
		perFile []bool // each a run with the same want
		want    []string
	}{
		{"a join frees a place the next ranked takes", []int64{1, 2, 5, 6, 10}, 4, both,
			[]string{"1 A.java:1-20 A", "5 C.java:1-10 C", "6 D.java:1-10 D", "10 G.java:1-10 G"}},
		{"the declaration ranked first keeps its place", []int64{2, 5, 1, 6}, 3, both,
			[]string{"1 A.java:1-20 A", "5 C.java:1-10 C", "6 D.java:1-10 D"}},
		{"a chunk read in place of a join joins too, within limit", []int64{1, 2, 3, 4, 5}, 2, both,
			[]string{"1 A.java:1-20 A", "3 B.java:1-30 B"}},
		{"an anchor first keeps its place (SearchPage)", []int64{6, 4, 3, 5}, 3, both,
			[]string{"6 D.java:1-10 D", "3 B.java:1-30 B", "5 C.java:1-10 C"}},
		{"the ranked run out", []int64{1, 2, 3, 4}, 3, both,
			[]string{"1 A.java:1-20 A", "3 B.java:1-30 B"}},
		{"a header alone and an id no chunk has", []int64{9, 99, 5}, 3, both,
			[]string{"9 F.java:1-3 ", "5 C.java:1-10 C"}},
		{"nothing to read", nil, 3, both, nil},
		{"per file, a file's second chunk gives its place to the next file", []int64{5, 7, 6, 10}, 3, per,
			[]string{"5 C.java:1-10 C", "6 D.java:1-10 D", "10 G.java:1-10 G"}},
		{"not per file, a file's second chunk takes a place", []int64{5, 7, 6}, 3, flat,
			[]string{"5 C.java:1-10 C", "7 C.java:11-20 C2", "6 D.java:1-10 D"}},
		{"per file, a header after its declaration joins it in its place", []int64{2, 1, 5}, 2, per,
			[]string{"1 A.java:1-20 A", "5 C.java:1-10 C"}},
		{"per file, a header after another chunk of its file is left out", []int64{8, 1, 5}, 2, per,
			[]string{"8 A.java:21-40 A2", "5 C.java:1-10 C"}},
		{"per file, a chunk after its file's joined header is left out (2 read in the join)", []int64{1, 2, 8, 5}, 2, per,
			[]string{"1 A.java:1-20 A", "5 C.java:1-10 C"}},
		{"per file, a header alone after its file's declaration is left out", []int64{11, 9, 5}, 2, per,
			[]string{"11 F.java:5-9 F2", "5 C.java:1-10 C"}},
		{"per file, the ranked run out short of limit", []int64{5, 7}, 3, per,
			[]string{"5 C.java:1-10 C"}},
	} {
		for _, perFile := range c.perFile {
			if got := read(c.ranked, c.limit, perFile); !slices.Equal(got, c.want) {
				t.Errorf("%s: Read(%v, %d, %t) = %q, want %q", c.name, c.ranked, c.limit, perFile, got, c.want)
			}
		}
	}
	if hits, err := s.Read(ctx, 1, []int64{3, 4}, 1, false); err != nil || len(hits) != 1 || hits[0].Content != "import b;\nclass B {}" {
		t.Fatalf("a joined hit's content is the header's, then the declaration's: %+v, %v", hits, err)
	}
}

// TestReached: what a declaration reaches in its file, in order (the rest of
// its lines, its members by their first line, then the functions, methods and
// constructors its own lines name as a word), each chunk once, its own left
// out; nothing for line 0, a line no declaration holds or a file without
// symbols. Holding gives each anchor its chunk, 0 for none.
func TestReached(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	chunks := map[int64]string{ // id: "start end content", all in g.go but 12 (o.go) and 40 (n.go)
		1: "1 3 type T struct{}", 2: "10 12 func (T) m() {}", 3: "20 22 type TT struct{}", 4: "24 26 func (TT) n() {}",
		5: "30 37 func (T) long() {", 6: "38 45 }",
		7: "50 57 func f() {", 8: "58 65 }",
		9: "70 74 func h() {\n\twalk()\n\tuse(visit)\n\tother()\n}", 10: "80 82 func walk() {}", 11: "84 86 func visit() {}",
		13: "90 94 func k() {\n\txwalk()\n\twalkx()\n\téwalk()\n}",
		14: "100 105 func fa() {\n\thelperA()\n}\nfunc fb() {\n\thelperB()\n}", 15: "110 112 func helperA() {}", 16: "114 116 func helperB() {}",
		17: "120 124 class C {", 18: "125 130 void run() { other() }", 19: "131 140 }",
		20: "150 157 short1(); long1", 21: "158 165 long1 end",
		22: "170 174 func (x *S) f2() { x.Do(); NewS() }", 23: "180 182 type S struct{}", 24: "184 186 func (S) Do() {}", 25: "188 190 func NewS() S {}",
		26: "200 208 func p() { walk() }",
		27: "210 212 func self() { self() }",
		28: "220 224 class D {", 29: "225 230 int x; }",
		30: "240 242 type U struct{}", 31: "250 252 func (U) z() {}", 32: "260 262 func (U) a() {}",
		33: "270 272 func q() { zh(); ah() }", 34: "280 282 func zh() {}", 35: "290 292 func ah() {}",
		36: "400 407 type V struct {", 37: "408 415 \tcb int // vhelp\n}", 38: "420 422 func vhelp() {}", 39: "430 432 func (V) m() {}",
	}
	ins := `INSERT INTO chunks(id, snapshot_id, path, start_line, end_line, content, content_hash) VALUES (12, 1, 'o.go', 1, 3, 'func other() {}', 'o'), (40, 1, 'n.go', 1, 3, 'x', 'n')`
	for id, c := range chunks {
		f := strings.SplitN(c, " ", 3)
		ins += fmt.Sprintf(", (%d, 1, 'g.go', %s, %s, '%s', 'h%d')", id, f[0], f[1], f[2], id)
	}
	// name qualified kind start end; U.a is inserted before U.z, and ah before zh.
	syms := `T T struct 1 3,m T.m method 10 12,TT TT struct 20 22,n TT.n method 24 26,long T.long method 30 45,f f function 50 65,
		h h function 70 74,walk walk function 80 82,visit visit function 84 86,k k function 90 94,fa fa function 100 102,fb fb function 103 105,
		helperA helperA function 110 112,helperB helperB function 114 116,C C class 120 140,run C.run method 125 130,
		long1 long1 type 150 165,short1 short1 function 150 152,f2 S.f2 method 170 174,S S struct 180 182,Do S.Do method 184 186,
		NewS NewS constructor 188 190,p p function 204 206,self self function 210 212,D D class 220 230,x D.x field 226 228,
		U U struct 240 242,a U.a method 260 262,z U.z method 250 252,q q function 270 272,ah ah function 290 292,zh zh function 280 282,
		V V struct 400 415,vhelp vhelp function 420 422,m V.m method 430 432`
	sins := `INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'o.go', 'other', 'other', 'function', 1, 3)`
	for _, s := range strings.Split(syms, ",") {
		f := strings.Fields(s)
		sins += fmt.Sprintf(", (1, 'g.go', '%s', '%s', '%s', %s, %s)", f[0], f[1], f[2], f[3], f[4])
	}
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`, ins, sins,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{DB: db}
	for _, c := range []struct {
		name string
		line int
		want []int64
	}{
		{"a type's members by their first line, a member's first chunk only, not TT's", 1, []int64{2, 5}},
		{"a long function's rest", 50, []int64{8}},
		{"a call and a name passed, not a function of another file", 70, []int64{10, 11}},
		{"no part of a word: xwalk, walkx, éwalk", 90, nil},
		{"its own lines, not its chunk's: fa, not fb", 100, []int64{15}},
		{"the innermost: C.run, not C", 125, nil},
		{"two from one line: the shorter", 150, nil},
		{"a method and a constructor named, not a struct", 170, []int64{24, 25}},
		{"a chunk's range past its content", 204, nil},
		{"its own chunk, though it names itself", 210, nil},
		{"a member's chunk that is its rest once", 220, []int64{29}},
		{"members by line, not as inserted", 240, []int64{31, 32}},
		{"named by line, not as inserted", 270, []int64{34, 35}},
		{"its rest, then its members, then what it names, though named first by line", 400, []int64{37, 39, 38}},
		{"line 0", 0, nil},
		{"a line no declaration holds", 300, nil},
	} {
		got, err := s.Reached(ctx, 1, []string{"g.go"}, []int{c.line})
		if err != nil || len(got) != 1 || !slices.Equal(got[0], c.want) {
			t.Errorf("%s: Reached(g.go:%d) = %v, %v; want %v", c.name, c.line, got, err, c.want)
		}
	}
	if got, err := s.Reached(ctx, 1, []string{"n.go", "g.go"}, []int{1, 50}); err != nil || len(got) != 2 || got[0] != nil || !slices.Equal(got[1], []int64{8}) {
		t.Errorf("a file without symbols, then another: %v, %v", got, err)
	}
	if got, err := s.Holding(ctx, 1, []string{"g.go", "g.go", "g.go"}, []int{1, 999, 10}); err != nil || !slices.Equal(got, []int64{1, 0, 2}) {
		t.Errorf("Holding: %v, %v; want [1 0 2], an anchor on no chunk 0", got, err)
	}
}
