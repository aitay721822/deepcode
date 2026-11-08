package bench

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/analyzer"
)

// coverage is how many of DeepWiki's topics head's wiki covers, and how many
// of its pages are about tests, CI, manifests or editor settings alone.
type coverage struct {
	Topics       int      `json:"topics"`
	Covered      int      `json:"covered"`
	Missing      []string `json:"missing"`
	ToolingPages int      `json:"testAndToolingPages"`
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
// headings (headingsOf).
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
		out = append(append(out, title), headingsOf(content)...)
	}
	return out, rows.Err()
}

// headingsOf are the "##" and "###" headings of a page's text (not "####"),
// outside code blocks, where "## " may be a shell comment. A block opens with
// three or more backticks or tildes, indented up to three spaces, and closes
// with a line of as many or more of the same and nothing else.
func headingsOf(content string) []string {
	var out []string
	fence := "" // the open block's mark
	for line := range strings.Lines(content) {
		m := fenceRe.FindStringSubmatch(line)
		switch {
		case m != nil && fence == "":
			fence = m[1]
		case m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(m[2]) == "":
			fence = ""
		case fence == "":
			if h, ok := strings.CutPrefix(line, "## "); ok || strings.HasPrefix(line, "### ") {
				out = append(out, strings.TrimSpace(strings.TrimPrefix(h, "### ")))
			}
		}
	}
	return out
}

// fenceRe matches a code block's fence line: its mark ("```", "~~~~") and
// what follows it on the line ("go").
var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})([^\n]*)")

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

// cover checks each topic against the headings.
func cover(topics []topic, heads []string) coverage {
	c := coverage{Topics: len(topics)}
	for _, tp := range topics {
		found := false
		for _, h := range heads {
			for _, w := range tp.words {
				found = found || holds(h, w)
			}
		}
		if found {
			c.Covered++
		} else {
			c.Missing = append(c.Missing, tp.name)
		}
	}
	return c
}

// holds reports whether heading holds word as a whole word, case and hyphens
// aside, a name's words counting (edge): WelcomeController and HTTPServer
// hold welcome and server, Auto Generating holds auto-generating, Unwelcome
// does not hold welcome.
func holds(heading, word string) bool {
	space := strings.NewReplacer("-", " ") // one byte for one, so that edge reads heading where h is
	h, w := space.Replace(lowerASCII(heading)), space.Replace(strings.ToLower(word))
	for i := 0; ; {
		j := strings.Index(h[i:], w)
		if j < 0 {
			return false
		}
		j += i
		if edge(heading, j) && edge(heading, j+len(w)) {
			return true
		}
		i = j + 1
	}
}

// edge reports whether a word may start or end at byte i of s: at an end of
// s, beside a byte that is no ASCII letter or digit (so beside any Chinese
// character), between a letter and a digit (OAuth|2, Base|64), before a
// capital that follows a small letter or digit (Welcome|Controller), before
// a capital that ends a run of them and is followed by a small letter
// (HTTP|Server, CI|Pipeline), or before an s after a run of capitals that
// ends a word (API|s, API|sController).
func edge(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	a, b := s[i-1], s[i]
	capital := func(c byte) bool { return 'A' <= c && c <= 'Z' }
	small := func(c byte) bool { return 'a' <= c && c <= 'z' }
	digit := func(c byte) bool { return '0' <= c && c <= '9' }
	next := byte(' ')
	if i+1 < len(s) {
		next = s[i+1]
	}
	return !alnum(a) || !alnum(b) || !digit(a) && digit(b) || (small(a) || digit(a)) && capital(b) ||
		capital(a) && capital(b) && small(next) || i >= 2 && capital(s[i-2]) && capital(a) && b == 's' && (!alnum(next) || capital(next))
}

// alnum reports whether c is an ASCII letter or digit.
func alnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// lowerASCII lowers s's ASCII letters only, so that its bytes stay where they are.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
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
}

// TestCiteLink: a citation's lines and link are aside, a bracket in its path kept.
func TestCiteLink(t *testing.T) {
	in := "See [a.go:1-2](https://x/blob/s/a.go#L1-L2), [app/\\[slug\\]/page.tsx:3-4](https://x/p#L3-L4) and [app/(g)/p.tsx:5-6](https://x/-/blob/s/app/(g)/p.tsx#L5-6), not [this](u)."
	if got := citeLink.ReplaceAllString(in, "[$1]"); got != "See [a.go], [app/\\[slug\\]/page.tsx] and [app/(g)/p.tsx], not [this](u)." {
		t.Errorf("got %q", got)
	}
}

// TestCover: a topic is covered when a heading holds one of its words as a
// whole word, a name's words counting and a hyphen as a space, and not inside
// a longer word; headings in code blocks do not count.
func TestCover(t *testing.T) {
	topics := []topic{{"Mux", []string{"mux"}}, {"CI", []string{"ci"}}, {"Context", []string{"route context"}}, {"app.param", []string{"app.param"}}}
	c := cover(topics, []string{"The chi.Mux router", "Circuit breakers", "Route Context and URLParam", "Using app.params"})
	if c.Covered != 2 || strings.Join(c.Missing, ",") != "CI,app.param" {
		t.Errorf("cover = %+v; want Mux and Context covered, not CI (in Circuit) or app.param (in app.params)", c)
	}
	for _, h := range []struct {
		heading, word string
		want          bool
	}{
		{"WelcomeController", "welcome", true}, {"Unwelcome", "welcome", false}, {"HTTPServer", "server", true}, {"HTTPServer", "http", true},
		{"CIPipeline", "ci", true}, {"CIRCUIT", "ci", false}, {"OAuth2Client", "client", true}, {"OAuth2 Login", "oauth", true},
		{"Base64 Encoding", "base", true}, {"Base64encode", "encode", false}, {"REST APIs", "api", true}, {"CIs", "ci", true},
		{"APIsController", "api", true}, {"APIsimple", "api", false}, {"Apis", "api", false}, {"İ server", "server", true},
		{"使用server", "server", true}, {"GracefulShutdown", "graceful", true}, {"Ungraceful", "graceful", false},
		{"Auto Generating Selectors", "auto-generating", true}, {"Mobile-BFF", "mobile bff", true}, {"Auto-generatings", "auto generating", false},
	} {
		if got := holds(h.heading, h.word); got != h.want {
			t.Errorf("holds(%q, %q) = %v", h.heading, h.word, got)
		}
	}
	page := "## One\n```sh\n## a comment\n```\n### Two\n#### Three\n  ~~~~\n## in tildes\n```\n## still in\n~~~~\n## Four\r\n" +
		"````\n```\n## in four\n```go\n````\n    ```\n## Five\n```\n## in three\n```go\n## still in three\n```  \n## Six\n"
	if got := headingsOf(page); !slices.Equal(got, []string{"One", "Two", "Four", "Five", "Six"}) {
		t.Errorf("headingsOf = %q, want One, Two, Four, Five and Six, nothing of the code blocks", got)
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
