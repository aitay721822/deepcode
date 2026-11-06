package vectorstore

import (
	"context"
	"path/filepath"
	"testing"

	"deepcode/internal/platform/storage"
)

// TestSearch: vectors rank by cosine similarity within their snapshot and
// model, a model keeps one dimension, and vectors survive encoding.
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
			(1, 1, 'a.go', 1, 2, 'x', 'h1'), (2, 1, 'b.go', 1, 2, 'y', 'h2'), (3, 2, 'c.go', 1, 2, 'z', 'h3')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &SQLite{DB: db}
	if err := s.Upsert(ctx, "m", map[int64][]float32{1: {1, 0}, 2: {3, 4}, 3: {0, 1}}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, 1, "m", []float32{0, 2}, 5)
	if err != nil || len(hits) != 2 || hits[0].ChunkID != 2 || hits[0].Score < 0.79 || hits[0].Score > 0.81 || hits[1].ChunkID != 1 {
		t.Fatalf("hits in snapshot 1: %+v %v", hits, err)
	}
	if hits, _ := s.Search(ctx, 1, "other", []float32{0, 1}, 5); len(hits) != 0 {
		t.Fatalf("another model's vectors: %+v", hits)
	}
	if err := s.Upsert(ctx, "m", map[int64][]float32{1: {1, 0, 0}}); err == nil {
		t.Fatal("stored a vector of another dimension")
	}
	if _, err := s.Search(ctx, 1, "m", []float32{1, 0, 0}, 5); err == nil {
		t.Fatal("searched with a query of another dimension")
	}
	if v := Decode(Encode([]float32{1.5, -2})); len(v) != 2 || v[0] != 1.5 || v[1] != -2 {
		t.Fatalf("round trip: %v", v)
	}
}
