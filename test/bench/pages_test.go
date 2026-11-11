package bench

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/test/bench/score"
)

// coverage is how many of DeepWiki's topics head's wiki covers, how many of
// its pages are about tests, CI, manifests or editor settings alone, and the
// code files no page holds (shape.Shape's).
type coverage struct {
	Topics       int      `json:"topics"`
	Covered      int      `json:"covered"`
	Missing      []string `json:"missing"`
	ToolingPages int      `json:"testAndToolingPages"`
	Unnamed      []string `json:"unnamedCode"`
}

// citeLink is a citation's link, as the wiki writes it; its file is group 1.
var citeLink = regexp.MustCompile(`\[((?:[^\]\\\n]|\\.)*?):\d+-\d+\]\(\S*?#L\d+-L?\d+\)`) // a path may hold parentheses

// rewritten counts head's pages that are not base's page of the same title
// with the same text, their citations' lines and links aside: the pages the
// update wrote, as a carried page's citations follow their lines to head.
func rewritten(ctx context.Context, db *sql.DB, base, head int64) (int, error) {
	texts := map[int64]map[string]map[string]bool{base: {}, head: {}} // by snapshot, title: texts
	for snap, pages := range texts {
		rows, err := db.QueryContext(ctx, `SELECT title, content FROM wiki_pages WHERE snapshot_id = ?`, snap)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var title, content string
			if err := rows.Scan(&title, &content); err != nil {
				rows.Close()
				return 0, err
			}
			if pages[title] == nil {
				pages[title] = map[string]bool{}
			}
			pages[title][citeLink.ReplaceAllString(content, "[$1]")] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}
	n := 0
	for title, now := range texts[head] {
		for text := range now {
			if !texts[base][title][text] {
				n++
			}
		}
	}
	return n, nil
}

// headings are the titles of the snapshot's pages and their "##" and "###"
// headings (score.Headings).
func headings(ctx context.Context, db *sql.DB, snap int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT title, content FROM wiki_pages WHERE snapshot_id = ?`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var title, content string
		if err := rows.Scan(&title, &content); err != nil {
			return nil, err
		}
		out = append(append(out, title), score.Headings(content)...)
	}
	return out, rows.Err()
}

// span is the median and the most lines a citation of the snapshot's pages
// spans, the median of an even count the mean of its middle two, rounded
// down: DeepWiki cites a function or a few lines, and a page citing a whole
// file is written again whenever any line of it changes.
func span(ctx context.Context, db *sql.DB, snap int64) (citationSpan, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.end_line - c.start_line + 1 FROM wiki_citations c JOIN wiki_pages p ON p.id = c.page_id
		WHERE p.snapshot_id = ? ORDER BY 1`, snap)
	if err != nil {
		return citationSpan{}, err
	}
	defer rows.Close()
	var n []int
	for rows.Next() {
		var lines int
		if err := rows.Scan(&lines); err != nil {
			return citationSpan{}, err
		}
		n = append(n, lines)
	}
	if len(n) == 0 {
		return citationSpan{}, rows.Err()
	}
	return citationSpan{Median: (n[(len(n)-1)/2] + n[len(n)/2]) / 2, Max: n[len(n)-1]}, rows.Err()
}

// citationSpan is how many lines a page citation spans: the median and the most.
type citationSpan struct {
	Median int `json:"median"`
	Max    int `json:"max"`
}

// toolingPages counts the snapshot's pages that cite only tests, CI,
// manifests, editor settings and the like.
func toolingPages(ctx context.Context, db *sql.DB, snap int64) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.page_id, c.path FROM wiki_citations c JOIN wiki_pages p ON p.id = c.page_id WHERE p.snapshot_id = ?`, snap)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cited := map[int64][]string{} // by page
	for rows.Next() {
		var page int64
		var p string
		if err := rows.Scan(&page, &p); err != nil {
			return 0, err
		}
		cited[page] = append(cited[page], p)
	}
	return onlyTooling(cited), rows.Err()
}

// onlyTooling counts the pages, given the files each cites, that cite tooling alone.
func onlyTooling(cited map[int64][]string) int {
	n := 0
	for _, paths := range cited {
		if !slices.ContainsFunc(paths, func(p string) bool { return !tooling(p) }) {
			n++
		}
	}
	return n
}

// tooling reports whether p is a test, CI, manifest or editor file and the like.
func tooling(p string) bool {
	aside := analyzer.Aside(p)
	return aside == analyzer.Tests || aside == analyzer.Tooling || aside == "" && analyzer.Config(p)
}

// cover is how many of topics the headings cover (score.Cover).
func cover(topics []score.Topic, heads []string) coverage {
	n, missing := score.Cover(topics, heads)
	return coverage{Topics: len(topics), Covered: n, Missing: missing}
}

// TestRewritten: a page whose citations only moved, to other lines at another
// commit, was not rewritten; one whose text changed, or a new one, was.
func TestRewritten(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	link := func(sha string, n int) string {
		return fmt.Sprintf("See [a.go:%d-%d](https://x/blob/%s/a.go#L%[1]d-L%[1]d).", n, n, sha)
	}
	for _, q := range []string{`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://x', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'b', 'ready'), (2, 1, 'h', 'ready')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, position, content) VALUES (1, '1', 'Moved', 0, '` + link("b", 1) + `'),
			(1, '2', 'Edited', 1, 'old'), (2, '1', 'Moved', 0, '` + link("h", 3) + `'), (2, '2', 'Edited', 1, 'new'), (2, '3', 'Added', 2, 'x')`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := rewritten(ctx, db, 1, 2); err != nil || n != 2 {
		t.Errorf("%d pages rewritten (%v), want 2", n, err)
	}
	// Head's citations span 1, 3, 10 and 12 lines; base's 50 do not count.
	if _, err := db.Exec(`INSERT INTO wiki_citations(page_id, path, start_line, end_line) VALUES (3, 'a.go', 1, 1), (3, 'a.go', 1, 10), (4, 'b.go', 5, 7),
		(5, 'c.go', 1, 12), (1, 'a.go', 1, 50)`); err != nil {
		t.Fatal(err)
	}
	if s, err := span(ctx, db, 2); err != nil || s != (citationSpan{6, 12}) {
		t.Errorf("span %+v (%v), want median 6 and at most 12", s, err)
	}
}

// TestCiteLink: a citation's lines and link are aside, a bracket in its path kept.
func TestCiteLink(t *testing.T) {
	in := "See [a.go:1-2](https://x/blob/s/a.go#L1-L2), [app/\\[slug\\]/page.tsx:3-4](https://x/p#L3-L4) and [app/(g)/p.tsx:5-6](https://x/-/blob/s/app/(g)/p.tsx#L5-6), not [this](u)."
	if got := citeLink.ReplaceAllString(in, "[$1]"); got != "See [a.go], [app/\\[slug\\]/page.tsx] and [app/(g)/p.tsx], not [this](u)." {
		t.Errorf("got %q", got)
	}
}

// TestTooling: test, CI, manifest and editor files are tooling, code, docs and
// examples are not, and a page is a tooling page when it cites tooling alone.
func TestTooling(t *testing.T) {
	for p, want := range map[string]bool{"src/test/java/AppTest.java": true, ".github/workflows/ci.yml": true, "pom.xml": true, "README.md": true,
		"src/main/java/App.java": false, "docs/guide.md": false, "examples/rest/main.go": false} {
		if got := tooling(p); got != want {
			t.Errorf("%s counts as tooling: %v", p, got)
		}
	}
	if n := onlyTooling(map[int64][]string{1: {"a_test.go", "go.mod"}, 2: {"a_test.go", "main.go"}, 3: {"main.go"}}); n != 1 {
		t.Errorf("%d pages cite tooling alone, want 1", n)
	}
}
