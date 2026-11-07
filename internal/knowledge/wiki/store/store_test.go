package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"deepcode/internal/platform/storage"
)

// TestShownPicksSnapshot: a finished wiki wins over the published snapshot's
// pages so far, the published snapshot over newer unfinished ones, and a
// pruned snapshot never shows; without pages there is none to show.
func TestShownPicksSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{DB: db}
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r')`)
	exec(`INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status) VALUES
		(1, 1, 'a', 'ready', 'generating'), (2, 1, 'b', 'ready', 'failed'), (3, 1, 'c', 'pruned', 'ready')`)
	exec(`UPDATE repositories SET ready_snapshot_id = 1 WHERE id = 1`)
	if _, err := s.Shown(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no pages: %v", err)
	}
	exec(`INSERT INTO wiki_pages(snapshot_id, slug, title, position) VALUES (1, 'p', 'P', 0), (2, 'p', 'P', 0), (3, 'p', 'P', 0)`)
	if w, err := s.Shown(ctx, 1); err != nil || w.ID != 1 {
		t.Fatalf("no finished wiki: snapshot %d shown (%v), want the published 1", w.ID, err)
	}
	exec(`UPDATE snapshots SET wiki_status = 'ready' WHERE id = 2`)
	if w, err := s.Shown(ctx, 1); err != nil || w.ID != 2 || w.CommitSHA != "b" || w.WikiStatus != "ready" {
		t.Fatalf("snapshot %+v shown (%v), want 2 with the finished wiki", w, err)
	}
}
