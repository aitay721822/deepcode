package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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

// TestPlanRoundTrip (G22): a page's files and the wiki's labels read back as
// stored, a page stored before them has none, a wiki before labels the zero
// Labels; Hashes reads the named files' contents' hashes, a missing one none.
func TestPlanRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'a', 'ready'), (2, 1, 'b', 'ready');
		INSERT INTO file_contents(hash, content) VALUES ('h1', 'x'), ('h2', 'y');
		INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, 'a.go', 'go', 'h1', 1, 1), (1, 'b.go', 'go', 'h2', 1, 1);
		INSERT INTO wiki_pages(snapshot_id, slug, title, position) VALUES (2, 'old', 'Old', 0)`); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	files := PageFiles{Chosen: []string{"a.go"}, Placed: []string{"b.go"}, Units: []string{"./"}}
	labels := Labels{Stack: "Technologie", OtherFiles: "Weitere Dateien"}
	if err := s.Add(ctx, 1, "Deutsch", labels, []NewPage{{Slug: "1", Title: "Ü", Parent: -1}, {Slug: "2", Title: "A", Parent: 0, Files: files}}); err != nil {
		t.Fatal(err)
	}
	pages, err := s.Pages(ctx, 1)
	got, gotErr := s.Labels(ctx, 1)
	old, _ := s.Pages(ctx, 2)
	none, noneErr := s.Labels(ctx, 2)
	if err != nil || gotErr != nil || noneErr != nil || len(pages) != 2 || !reflect.DeepEqual(*pages[1].Files, files) || pages[1].Parent != 0 ||
		got != labels || old[0].Files != nil || none != (Labels{}) {
		t.Fatalf("pages %+v, labels %+v and %+v, old %+v (%v %v %v)", pages, got, none, old, err, gotErr, noneErr)
	}
	if h, err := s.Hashes(ctx, 1, []string{"a.go", "b.go", "gone.go"}); err != nil || !reflect.DeepEqual(h, map[string]string{"a.go": "h1", "b.go": "h2"}) {
		t.Fatalf("hashes %v (%v)", h, err)
	}
}

// TestDeclsTexts: Decls is the named files' types and functions in line
// order, no method among them (so no C# top-level Main); Texts is the named
// files' texts, a missing one none.
func TestDeclsTexts(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'a', 'ready');
		INSERT INTO file_contents(hash, content) VALUES ('h1', 'x'), ('h2', 'y');
		INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES
			(1, 'a.go', 'go', 'h1', 1, 1), (1, 'b.cs', 'csharp', 'h2', 1, 1), (1, 'c.go', 'go', 'h2', 1, 1);
		INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'a.go', 'Run', 'Run', 'function', 5, 6), (1, 'a.go', 'T', 'T', 'struct', 2, 3), (1, 'a.go', 'M', 'T.M', 'method', 3, 3),
			(1, 'b.cs', 'Main', 'Main', 'method', 1, 1), (1, 'c.go', 'C', 'C', 'function', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	decls, err := s.Decls(ctx, 1, []string{"a.go", "b.cs"})
	texts, textErr := s.Texts(ctx, 1, []string{"a.go", "gone.go"})
	if want := map[string][]Decl{"a.go": {{"T", "struct", 2}, {"Run", "function", 5}}}; err != nil || textErr != nil || !reflect.DeepEqual(decls, want) ||
		!reflect.DeepEqual(texts, map[string]string{"a.go": "x"}) {
		t.Fatalf("decls %+v (%v), texts %v (%v)", decls, err, texts, textErr)
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
