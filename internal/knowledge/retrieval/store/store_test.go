package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"deepcode/internal/platform/storage"
)

// TestRead: a header ranked with its declaration is read as one hit in the
// place of the first, and the places joins free go to the chunks ranked after,
// up to limit, the places before unchanged; a declaration ranked first takes
// its header in its own place; a header with no declaration after it, and an
// id no chunk has, are read as they are.
func TestRead(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// A.java and B.java: a header (lines 1-5, 1-4) and the declaration after it;
	// F.java: a header alone; C, D and G: a declaration from line 1.
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
		`INSERT INTO chunks(id, snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES
			(1, 1, 'A.java', '', 1, 5, 'import a;', 'h1'), (2, 1, 'A.java', 'A', 6, 20, 'class A {}', 'h2'),
			(3, 1, 'B.java', '', 1, 4, 'import b;', 'h3'), (4, 1, 'B.java', 'B', 5, 30, 'class B {}', 'h4'),
			(5, 1, 'C.java', 'C', 1, 10, 'class C {}', 'h5'), (6, 1, 'D.java', 'D', 1, 10, 'class D {}', 'h6'),
			(9, 1, 'F.java', '', 1, 3, 'import f;', 'h9'), (10, 1, 'G.java', 'G', 1, 10, 'class G {}', 'h10')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{DB: db}
	read := func(ranked []int64, limit int) []string {
		t.Helper()
		hits, err := s.Read(ctx, 1, ranked, limit)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range hits {
			out = append(out, fmt.Sprintf("%d %s:%d-%d %s", h.ChunkID, h.Path, h.StartLine, h.EndLine, h.Symbol))
		}
		return out
	}
	for _, c := range []struct {
		name   string
		ranked []int64
		limit  int
		want   []string
	}{
		{"a join frees a place the next ranked takes", []int64{1, 2, 5, 6, 10}, 4,
			[]string{"1 A.java:1-20 A", "5 C.java:1-10 C", "6 D.java:1-10 D", "10 G.java:1-10 G"}},
		{"the declaration ranked first keeps its place", []int64{2, 5, 1, 6}, 3,
			[]string{"1 A.java:1-20 A", "5 C.java:1-10 C", "6 D.java:1-10 D"}},
		{"a chunk read in place of a join joins too, within limit", []int64{1, 2, 3, 4, 5}, 2,
			[]string{"1 A.java:1-20 A", "3 B.java:1-30 B"}},
		{"an anchor first keeps its place (SearchPage)", []int64{6, 4, 3, 5}, 3,
			[]string{"6 D.java:1-10 D", "3 B.java:1-30 B", "5 C.java:1-10 C"}},
		{"the ranked run out", []int64{1, 2, 3, 4}, 3,
			[]string{"1 A.java:1-20 A", "3 B.java:1-30 B"}},
		{"a header alone and an id no chunk has", []int64{9, 99, 5}, 3,
			[]string{"9 F.java:1-3 ", "5 C.java:1-10 C"}},
		{"nothing to read", nil, 3, nil},
	} {
		if got := read(c.ranked, c.limit); !slices.Equal(got, c.want) {
			t.Errorf("%s: Read(%v, %d) = %q, want %q", c.name, c.ranked, c.limit, got, c.want)
		}
	}
	if hits, err := s.Read(ctx, 1, []int64{3, 4}, 1); err != nil || len(hits) != 1 || hits[0].Content != "import b;\nclass B {}" {
		t.Fatalf("a joined hit's content is the header's, then the declaration's: %+v, %v", hits, err)
	}
}
