package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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

// TestRootFiles: the top level's READMEs first, a Markdown one before the
// others and the shorter named first, then its manifests; none below it.
func TestRootFiles(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'a', 'ready');
		INSERT INTO file_contents(hash, content) VALUES ('h', 'x');
		INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES
			(1, 'go.mod', 'gomod', 'h', 1, 1), (1, 'README', 'text', 'h', 1, 1), (1, 'README.zh-CN.md', 'markdown', 'h', 1, 1),
			(1, 'README.md', 'markdown', 'h', 1, 1), (1, 'docs/README.md', 'markdown', 'h', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	files, err := (&Store{DB: db}).RootFiles(ctx, 1)
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	if want := "README.md README.zh-CN.md README go.mod"; err != nil || strings.Join(got, " ") != want {
		t.Errorf("root files %v (%v), want %s", got, err, want)
	}
}
