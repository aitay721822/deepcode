package vectorstore

import (
	"context"
	"path/filepath"
	"testing"

	"deepcode/internal/platform/storage"
)

// TestSearch: vectors rank by cosine similarity within their snapshot and
// model, ties by id, up to the limit; a model keeps one dimension, and vectors
// survive encoding.
func TestSearch(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'a'), (2, 1, 'b')`,
		`INSERT INTO chunks(id, snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 1, 'a.go', 1, 2, 'x', 'h1'), (2, 1, 'b.go', 1, 2, 'y', 'h2'), (3, 2, 'c.go', 1, 2, 'z', 'h3'), (4, 1, 'd.go', 1, 2, 'w', 'h4')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &SQLite{DB: db}
	if err := s.Upsert(ctx, "m", map[int64][]float32{1: {1, 0}, 2: {3, 4}, 3: {0, 1}, 4: {2, 0}}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, 1, "m", []float32{0, 2}, 5, nil)
	if err != nil || len(hits) != 3 || hits[0].ChunkID != 2 || hits[0].Score < 0.79 || hits[0].Score > 0.81 || hits[1].ChunkID != 1 || hits[2].ChunkID != 4 {
		t.Fatalf("hits in snapshot 1: %+v %v", hits, err)
	}
	if top, _ := s.Search(ctx, 1, "m", []float32{0, 2}, 1, nil); len(top) != 1 || top[0].ChunkID != 2 {
		t.Fatalf("a limit of 1: %+v", top)
	}
	// A weight is told the chunk's path and whether its file is generated
	// (b.go is; a.go and d.go have no source_files row), and moves a match
	// down: b.go's 0.96 halves below a.go's and d.go's 0.8. It never moves a
	// mismatch up: b.go's −0.6 stays −0.6.
	for _, q := range []string{`INSERT INTO file_contents(hash, content) VALUES ('h2', 'y')`,
		`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count, generated) VALUES (1, 'b.go', 'go', 'h2', 1, 2, 1)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	halfGenerated := func(path string, generated bool) float32 {
		seen = append(seen, path)
		if generated {
			return 0.5
		}
		return 1
	}
	if hits, err := s.Search(ctx, 1, "m", []float32{0.8, 0.6}, 5, halfGenerated); err != nil || len(hits) != 3 ||
		hits[0].ChunkID != 1 || hits[1].ChunkID != 4 || hits[2].ChunkID != 2 || len(seen) != 3 {
		t.Fatalf("weighed: %+v %v, paths %v", hits, err, seen)
	}
	if hits, _ := s.Search(ctx, 1, "m", []float32{-1, 0}, 5, halfGenerated); hits[0].ChunkID != 2 || hits[0].Score > -0.59 {
		t.Fatalf("a mismatch weighed up: %+v", hits)
	}
	if hits, _ := s.Search(ctx, 1, "other", []float32{0, 1}, 5, nil); len(hits) != 0 {
		t.Fatalf("another model's vectors: %+v", hits)
	}
	if err := s.Upsert(ctx, "m", map[int64][]float32{1: {1, 0, 0}}); err == nil {
		t.Fatal("stored a vector of another dimension")
	}
	if _, err := s.Search(ctx, 1, "m", []float32{1, 0, 0}, 5, nil); err == nil {
		t.Fatal("searched with a query of another dimension")
	}
	if v := Decode(Encode([]float32{1.5, -2})); len(v) != 2 || v[0] != 1.5 || v[1] != -2 {
		t.Fatalf("round trip: %v", v)
	}
}
