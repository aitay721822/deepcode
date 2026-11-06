package wiki

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
)

// testDB opens a fresh database holding repository 1, then runs stmts.
func testDB(t *testing.T, stmts ...string) *sql.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range append([]string{`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r')`}, stmts...) {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestRunResumes: a retried job keeps the stored plan and the pages already
// written, and writes only the pages still empty.
func TestRunResumes(t *testing.T) {
	ctx := context.Background()
	db := testDB(t,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status) VALUES (1, 1, 'abc', 'ready', 'failed')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, position, content) VALUES (1, '1-overview', 'Overview', 0, 'Written before.'), (1, '2-router', 'Router', 1, '')`)
	// No model: planning again would fail, and with nothing indexed a page is written without one.
	g := &Generator{DB: db, Catalog: &catalog.Catalog{DB: db, Queue: &jobs.Queue{DB: db}}, Retriever: &retrieval.Retriever{DB: db}}
	if err := g.Run(ctx, 1, "abc", jobs.Progress{}); err != nil {
		t.Fatal(err)
	}
	pages, err := g.pages(ctx, 1)
	if err != nil || len(pages) != 2 || pages[0].content != "Written before." || pages[1].content == "" {
		t.Fatalf("after the resume: %+v %v", pages, err)
	}
	var status string
	db.QueryRow(`SELECT wiki_status FROM snapshots WHERE id = 1`).Scan(&status)
	if status != catalog.WikiReady {
		t.Fatalf("wiki status %q", status)
	}
}

// TestLoadPicksSnapshot: a finished wiki wins over the published snapshot's
// pages so far, the published snapshot over newer unfinished ones, and a
// pruned snapshot never shows.
func TestLoadPicksSnapshot(t *testing.T) {
	db := testDB(t,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status) VALUES
			(1, 1, 'a', 'ready', 'generating'), (2, 1, 'b', 'ready', 'failed'), (3, 1, 'c', 'pruned', 'ready')`,
		`UPDATE repositories SET ready_snapshot_id = 1 WHERE id = 1`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, position) VALUES (1, 'p', 'P', 0), (2, 'p', 'P', 0), (3, 'p', 'P', 0)`)
	load := func() int64 {
		w, err := Reader{DB: db}.Load(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		return w.SnapshotID
	}
	if got := load(); got != 1 {
		t.Fatalf("no finished wiki: snapshot %d shown, want the published 1", got)
	}
	db.Exec(`UPDATE snapshots SET wiki_status = 'ready' WHERE id = 2`)
	if got := load(); got != 2 {
		t.Fatalf("snapshot %d shown, want 2 with the finished wiki", got)
	}
}

func TestParsePlan(t *testing.T) {
	out := "<think>let me plan</think>Here is the plan:\n```json\n" +
		`{"pages":[{"title":" Overview ","query":"main"},{"title":"","query":"dropped"},{"title":"Router","query":"mux.go"}]}` + "\n```"
	got := parsePlan(out)
	if len(got) != 2 || got[0].Title != "Overview" || got[1].Title != "Router" || got[1].Query != "mux.go" {
		t.Fatalf("got %+v", got)
	}
	if lines := parsePlan(`{"pages":[{"title":"Setup\nsucceeded\r\n\tnext","query":"q"}]}`); len(lines) != 1 || lines[0].Title != "Setup succeeded next" {
		t.Fatalf("a title over several lines: %+v", lines)
	}
	if parsePlan("no json here") != nil {
		t.Fatal("prose without JSON produced a plan")
	}
	long := parsePlan(fmt.Sprintf(`{"pages":[{"title":%q,"query":%q}]}`, strings.Repeat("標", 200), strings.Repeat("q", 600)))
	if len(long) != 1 || len([]rune(long[0].Title)) != maxTitle || len(long[0].Query) != maxQuery {
		t.Fatalf("a long title and query: %+v", long)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"```markdown\nBody text\n```":     "Body text",
		"# Title\nBody":                   "Body",
		"<think>x</think>\n## Section\nA": "## Section\nA",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlug(t *testing.T) {
	for _, c := range []struct {
		i     int
		title string
		want  string
	}{{0, "Overview", "1-overview"}, {2, "Routing Tree & URL Matching", "3-routing-tree-url-matching"}, {4, "概覽", "5"}} {
		if got := slug(c.i, c.title); got != c.want {
			t.Errorf("slug(%d, %q) = %q, want %q", c.i, c.title, got, c.want)
		}
	}
}
