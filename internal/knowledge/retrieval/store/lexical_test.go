package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"deepcode/internal/platform/storage"
)

// TestDefiningOrder: a chunk ranks by its best symbol, a name the question
// writes as code first, then a name fewer chunks hold, then the longer name.
func TestDefiningOrder(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// fromJson and deserialize are in two chunks each, widget in one, gadgetry
	// in five; chunk 8 defines both gadgetry and widget.
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
		`INSERT INTO chunks(id, snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 1, 'Gson.java', 1, 9, 'fromJson gadgetry', 'h1'), (2, 1, 'Expose.java', 1, 9, 'deserialize gadgetry', 'h2'),
			(3, 1, 'Context.java', 1, 9, 'deserialize gadgetry', 'h3'), (4, 1, 'Widget.java', 1, 9, 'widget', 'h4'),
			(5, 1, 'Gadgetry.java', 1, 9, 'gadgetry', 'h5'), (6, 1, 'Caller.java', 1, 9, 'fromJson gadgetry', 'h6'),
			(7, 1, 'under.py', 1, 9, 'x', 'h7'), (8, 1, 'Mixed.java', 1, 9, 'x', 'h8')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'Gson.java', 'fromJson', 'Gson.fromJson', 'method', 1, 1), (1, 'Gson.java', 'fromJson', 'Gson.fromJson', 'method', 2, 2),
			(1, 'Expose.java', 'deserialize', 'Expose.deserialize', 'method', 1, 1),
			(1, 'Context.java', 'deserialize', 'Context.deserialize', 'method', 1, 1),
			(1, 'Widget.java', 'widget', 'widget', 'function', 1, 1), (1, 'Gadgetry.java', 'gadgetry', 'gadgetry', 'function', 1, 1),
			(1, 'under.py', '___', '___', 'function', 1, 1),
			(1, 'Mixed.java', 'gadgetry', 'gadgetry', 'function', 1, 1), (1, 'Mixed.java', 'widget', 'widget', 'function', 2, 2)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{DB: db}
	for _, c := range []struct {
		names, code []string
		want        []int64
	}{
		{[]string{"fromJson", "deserialize"}, []string{"fromJson"}, []int64{1, 2, 3}}, // code first, though shorter
		{[]string{"fromJson", "deserialize"}, nil, []int64{2, 3, 1}},                  // as common: the longer first
		{[]string{"gadgetry", "widget"}, nil, []int64{4, 8, 5}},                       // rarer first, though shorter
		{[]string{"gadgetry", "widget"}, []string{"gadgetry"}, []int64{5, 8, 4}},      // code before rarer
		{[]string{"gadgetry", "widget", "fromJson"}, []string{"gadgetry", "fromJson"}, // a chunk ranks by its best
			[]int64{1, 5, 8, 4}}, // symbol: 8's gadgetry, not code from it and rarity from its widget
		{[]string{"___"}, nil, []int64{7}},                 // a name of no words still ranks
		{[]string{"___", "widget"}, nil, []int64{4, 8, 7}}, // but after any word
	} {
		if got, err := s.Defining(ctx, 1, c.names, c.code, nil, 10); err != nil || !slices.Equal(got, c.want) {
			t.Errorf("Defining(%v, code %v) = %v %v, want %v", c.names, c.code, got, err, c.want)
		}
	}
}
