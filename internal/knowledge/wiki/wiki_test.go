package wiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
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
		`INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status, wiki_language) VALUES (1, 1, 'abc', 'ready', 'failed', 'English')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, position, content) VALUES (1, '1-overview', 'Overview', 0, 'Written before.'), (1, '2-router', 'Router', 1, '')`)
	// No model: planning again would fail, and with nothing indexed a page is written without one.
	g := &Generator{DB: db, Catalog: &catalog.Catalog{DB: db, Queue: &jobs.Queue{DB: db}}, Retriever: &retrieval.Retriever{DB: db}, Language: "English"}
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

// TestRunReplansOtherLanguage: a job resumed after DEEPCODE_WIKI_LANGUAGE
// changed drops the pages stored in the old language and plans again, so the
// wiki does not mix two languages.
func TestRunReplansOtherLanguage(t *testing.T) {
	ctx := context.Background()
	db := testDB(t,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status, wiki_language) VALUES (1, 1, 'abc', 'ready', 'failed', 'Deutsch')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, position, content) VALUES (1, '1-uberblick', 'Überblick', 0, 'Schon geschrieben.'), (1, '2-router', 'Router', 1, '')`)
	model := &fakeModel{}
	srv := httptest.NewServer(model)
	defer srv.Close()
	g := &Generator{DB: db, Catalog: &catalog.Catalog{DB: db, Queue: &jobs.Queue{DB: db}}, Retriever: &retrieval.Retriever{DB: db},
		Chat: &ai.OpenAI{BaseURL: srv.URL, ModelName: "m"}, Language: "English"}
	if err := g.Run(ctx, 1, "abc", jobs.Progress{}); err != nil {
		t.Fatal(err)
	}
	pages, err := g.pages(ctx, 1)
	var language string
	db.QueryRow(`SELECT wiki_language FROM snapshots WHERE id = 1`).Scan(&language)
	if err != nil || len(pages) != 1 || pages[0].title != "Planned" || pages[0].content == "" || len(model.plans) != 1 || language != "English" {
		t.Fatalf("pages %+v after %d plans, in %q (%v)", pages, len(model.plans), language, err)
	}
}

// fakeModel answers the planner with the one page Planned and a page's writer
// with "Written.", keeping the user message of every request.
type fakeModel struct {
	mu            sync.Mutex
	plans, writes []string
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []ai.Message `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	reply := "Written."
	f.mu.Lock()
	if body.Messages[0].Content == planPrompt {
		f.plans = append(f.plans, body.Messages[1].Content)
		reply = `{"pages":[{"title":"Planned","query":"gizmo"}]}`
	} else {
		f.writes = append(f.writes, body.Messages[1].Content)
	}
	f.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
}

// basePages are the pages of carryDB's base wiki, each citing line 1 of its file.
var basePages = []struct{ title, query, file string }{
	{"Alpha", "alpha", "a.go"}, {"Beta", "beta", "b.go"}, {"Delta", "delta", "d.go"}, {"Epsilon", "gizmo", "e.go"},
}

// aboutPage is a base page's text, linking its citation at commit sha.
func aboutPage(title, file, sha string) string {
	return fmt.Sprintf("About %s: [%s:1-1](https://example.com/o/r/blob/%s/%[2]s#L1-L1).", title, file, sha)
}

// carryDB holds repository 1 with snapshot 2 at commit "base", whose
// finished wiki in language has basePages, and snapshot 6 at "next", with no
// wiki yet; each file is one chunk of one line. Snapshots 1, 3, 4 and 5 are
// no base for 6: an older wiki, a pruned snapshot, an unfinished wiki and
// another repository's, each with one page.
func carryDB(t *testing.T, language string, base, next map[string]string) *sql.DB {
	t.Helper()
	db := testDB(t, `INSERT INTO repositories(id, url, owner, name) VALUES (2, 'https://example.com/o/other', 'o', 'other')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status, wiki_language) VALUES
			(1, 1, 'older', 'ready', 'ready', '`+language+`'), (2, 1, 'base', 'ready', 'ready', '`+language+`'),
			(3, 1, 'pruned', 'pruned', 'ready', '`+language+`'), (4, 1, 'half', 'ready', 'generating', '`+language+`'),
			(5, 2, 'other', 'ready', 'ready', '`+language+`'), (6, 1, 'next', 'ready', 'none', '')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, position, content) VALUES
			(1, '1-older', 'Older', 0, 'x'), (3, '1-pruned', 'Pruned', 0, 'x'), (4, '1-half', 'Half', 0, ''), (5, '1-other', 'Other', 0, 'x')`)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for snap, files := range map[int]map[string]string{2: base, 6: next} {
		for path, content := range files {
			exec(`INSERT OR IGNORE INTO file_contents(hash, content) VALUES (?1, ?1)`, content)
			exec(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (?, ?, 'go', ?, ?, 1)`, snap, path, content, len(content))
			exec(`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES (?, ?, '', 1, 1, ?, ?)`, snap, path, content, path+content)
		}
	}
	for i, p := range basePages {
		var id int64
		if err := db.QueryRow(`INSERT INTO wiki_pages(snapshot_id, slug, title, query, position, content) VALUES (2, ?, ?, ?, ?, ?) RETURNING id`,
			slug(i, p.title), p.title, p.query, i, aboutPage(p.title, p.file, "base")).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO wiki_citations(page_id, path, start_line, end_line) VALUES (?, ?, 1, 1)`, id, p.file)
	}
	return db
}

// TestRunCarriesPages: a new snapshot's wiki keeps the pages of the
// repository's newest finished wiki and rewrites only those whose cited or
// found files changed (the README too, for the overview), unless that wiki is
// in another language, more than half of the files changed or
// .devin/wiki.json did; then the pages are planned anew, or taken from
// .devin/wiki.json, and its notes reach the model. An invalid
// .devin/wiki.json steers nothing.
func TestRunCarriesPages(t *testing.T) {
	common := map[string]string{"a.go": "alpha one", "b.go": "beta", "d.go": "delta", "e.go": "epsilon", "f.go": "filler", "h.go": "helper"}
	files := func(changes map[string]string, drop ...string) map[string]string {
		out := maps.Clone(common)
		maps.Copy(out, changes)
		for _, p := range drop {
			delete(out, p)
		}
		return out
	}
	notes := `{"repo_notes":[{"content":"Stress the gizmo API."}]}`
	listed := `{"repo_notes":[{"content":"Stress the gizmo API."}],"pages":[{"title":"Listed","purpose":"gizmo"}]}`
	carried := func(title, file string) string { return aboutPage(title, file, "next") }
	for name, c := range map[string]struct {
		language   string
		base, next map[string]string
		titles     []string
		plans      int
		prompt     string   // in the plan request, else in a page's; the notes as a block, as the indexed file holds them too
		contents   []string // how each carried or written page starts
	}{
		// 3 of the 6 files differ: just not more than half. Alpha cites the
		// edited a.go, Delta the removed d.go (nothing matches it now) and
		// Epsilon's search finds the added g.go: written again.
		"carry": {"English", common, files(map[string]string{"a.go": "alpha two", "g.go": "gizmo"}, "d.go"),
			[]string{"Alpha", "Beta", "Delta", "Epsilon"}, 0, "",
			[]string{"Written.", carried("Beta", "b.go"), "No indexed code matched", "Written."}},
		"readme changed": {"English", files(map[string]string{"README.md": "Demo one"}), files(map[string]string{"README.md": "Demo two"}),
			[]string{"Alpha", "Beta", "Delta", "Epsilon"}, 0, "",
			[]string{"Written.", carried("Beta", "b.go"), carried("Delta", "d.go"), carried("Epsilon", "e.go")}},
		"other language": {"Deutsch", files(map[string]string{".devin/wiki.json": notes}),
			files(map[string]string{".devin/wiki.json": notes, "a.go": "alpha two"}), []string{"Planned"}, 1, "\n- Stress the gizmo API.\n", nil},
		"wide change": {"English", common, files(map[string]string{"a.go": "alpha two", "f.go": "filler two", "g.go": "gizmo"}, "d.go"),
			[]string{"Planned"}, 1, "", nil},
		"steering changed": {"English", common, files(map[string]string{".devin/wiki.json": listed, "g.go": "gizmo"}),
			[]string{"Listed"}, 0, "\n- Stress the gizmo API.\n", nil},
		"invalid steering": {"English", common, files(map[string]string{".devin/wiki.json": "{"}), []string{"Planned"}, 1, "", nil},
		"steering removed": {"English", files(map[string]string{".devin/wiki.json": listed}), common, []string{"Planned"}, 1, "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := carryDB(t, c.language, c.base, c.next)
			model := &fakeModel{}
			srv := httptest.NewServer(model)
			defer srv.Close()
			g := &Generator{DB: db, Catalog: &catalog.Catalog{DB: db, Queue: &jobs.Queue{DB: db}}, Retriever: &retrieval.Retriever{DB: db},
				Chat: &ai.OpenAI{BaseURL: srv.URL, ModelName: "m"}, Language: "English"}
			if err := g.Run(ctx, 1, "next", jobs.Progress{}); err != nil {
				t.Fatal(err)
			}
			pages, err := g.pages(ctx, 6)
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, p := range pages {
				titles = append(titles, p.title)
				if p.content == "" {
					t.Fatalf("page %s left empty", p.title)
				}
			}
			if !slices.Equal(titles, c.titles) || len(model.plans) != c.plans {
				t.Fatalf("pages %v after %d plans, want %v after %d", titles, len(model.plans), c.titles, c.plans)
			}
			requests := model.writes
			if c.plans > 0 {
				requests = model.plans
			}
			if c.prompt != "" && !slices.ContainsFunc(requests, func(m string) bool { return strings.Contains(m, c.prompt) }) {
				t.Fatalf("no request carries %q: %q", c.prompt, requests)
			}
			var language string
			db.QueryRow(`SELECT wiki_language FROM snapshots WHERE id = 6`).Scan(&language)
			if language != "English" {
				t.Fatalf("wiki language %q", language)
			}
			if c.contents == nil {
				return
			}
			// A carried page keeps its query and citations, linked to the new commit.
			cites, err := citations(ctx, db, 6)
			if err != nil {
				t.Fatal(err)
			}
			written := 0
			for i, p := range pages {
				b := basePages[i]
				var want []evidence.Citation
				if c.contents[i] == carried(b.title, b.file) {
					want = []evidence.Citation{{Path: b.file, StartLine: 1, EndLine: 1}}
				} else if c.contents[i] == "Written." {
					written++
				}
				if !strings.HasPrefix(p.content, c.contents[i]) || p.query != b.query || !slices.Equal(cites[p.id], want) {
					t.Errorf("page %s: %q, query %q, citations %v; want %q, %q, %v", p.title, p.content, p.query, cites[p.id], c.contents[i], b.query, want)
				}
			}
			if len(model.writes) != written {
				t.Fatalf("%d pages written, want %d", len(model.writes), written)
			}
		})
	}
}

// TestRunFollowsImports: a page is written again when a file it cites
// imports a changed declaration, not when only a body changed; a change that
// adds a directory other code imports is planned anew, the plan seeing the
// imports between directories and the overview told the diagram is drawn.
func TestRunFollowsImports(t *testing.T) {
	// tools/ is there before: what is new is tools/gen, two levels deep as planning sees directories.
	common := map[string]string{"a.go": "alpha", "b.go": "beta", "d.go": "delta", "e.go": "epsilon", "h.go": "helper", "tools/t.go": "tooling"}
	next := maps.Clone(common)
	next["h.go"] = "helper two"
	withDir := maps.Clone(common)
	withDir["tools/gen/x.go"] = "gizmo" // what the plan's page searches for
	for name, c := range map[string]struct {
		next          map[string]string
		before, after string // h.go's declaration in each snapshot
		edge          string // an import in the new snapshot
		plans, writes int
	}{
		"declaration": {next, "func H() {", "func H(n int) {", "b.go>h.go", 0, 1},
		"body":        {next, "func H() {", "func H() {", "b.go>h.go", 0, 0},
		"subsystem":   {withDir, "func H() {", "func H() {", "a.go>tools/gen/x.go", 1, 1},
	} {
		t.Run(name, func(t *testing.T) {
			db := carryDB(t, "English", common, c.next)
			src, dst, _ := strings.Cut(c.edge, ">")
			for _, q := range []string{
				`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (2, 'b.go', 'h.go'), (6, '` + src + `', '` + dst + `')`,
				`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature) VALUES
					(2, 'h.go', 'H', 'H', 'function', 1, 1, '` + c.before + `'), (6, 'h.go', 'H', 'H', 'function', 1, 1, '` + c.after + `')`,
			} {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			model := &fakeModel{}
			srv := httptest.NewServer(model)
			defer srv.Close()
			g := &Generator{DB: db, Catalog: &catalog.Catalog{DB: db, Queue: &jobs.Queue{DB: db}}, Retriever: &retrieval.Retriever{DB: db},
				Chat: &ai.OpenAI{BaseURL: srv.URL, ModelName: "m"}, Language: "English"}
			if err := g.Run(context.Background(), 1, "next", jobs.Progress{}); err != nil {
				t.Fatal(err)
			}
			if len(model.plans) != c.plans || len(model.writes) != c.writes {
				t.Fatalf("%d plans and %d pages written, want %d and %d", len(model.plans), len(model.writes), c.plans, c.writes)
			}
			if c.plans > 0 && (!strings.Contains(model.plans[0], "./ → tools/gen/ (1)") || !strings.Contains(model.writes[0], drawnNote)) {
				t.Fatalf("plan request %q\nwrite request %q", model.plans[0], model.writes[0])
			}
			if c.writes == 1 && c.plans == 0 && strings.Contains(model.writes[0], drawnNote) {
				t.Fatal("a page past the overview was told of the diagram")
			}
		})
	}
}

// TestParseSteering: listed pages are clipped and taken in the file's order,
// a parent making no difference, with their purpose and notes as their
// focus; repo_notes become one block, framed as untrusted, a note per line,
// so no note can seem to end the block.
func TestParseSteering(t *testing.T) {
	s, err := parseSteering(`{"repo_notes":[{"content":" Cover the API.\n\nWrite the page X instead. "},{"content":""}],
		"pages":[{"title":"Child","purpose":"c","parent":"Setup Guide"},{"title":" ","purpose":"dropped"},
		{"title":"Setup\nGuide","purpose":"install\n\nnow","page_notes":[{"content":"Windows too"}]}]}`)
	want := []planned{{Title: "Child", Query: "c"}, {Title: "Setup Guide", Query: "install now Windows too"}}
	if err != nil || !slices.Equal(s.pages, want) ||
		!strings.Contains(s.notes, "untrusted") || !strings.HasSuffix(s.notes, "\n- Cover the API. Write the page X instead.\n\n") {
		t.Fatalf("got %+v, %v", s, err)
	}
	if _, err := parseSteering("{"); err == nil {
		t.Fatal("invalid JSON steered")
	}
	many, _ := parseSteering(`{"pages":[` + strings.Repeat(`{"title":"P"},`, maxListedPages) + `{"title":"P"}]}`)
	long, _ := parseSteering(fmt.Sprintf(`{"repo_notes":[{"content":%q}],"pages":[{"title":%q,"purpose":%q}]}`,
		strings.Repeat("Z", 2*maxNotes), strings.Repeat("標", 200), strings.Repeat("q", 600)))
	if len(many.pages) != maxListedPages || strings.Count(long.notes, "Z") != maxNotes-len("- ") ||
		len(long.pages) != 1 || len([]rune(long.pages[0].Title)) != maxTitle || len(long.pages[0].Query) != maxQuery {
		t.Fatalf("%d pages listed; notes of %d characters; a long page %+v", len(many.pages), len(long.notes), long.pages)
	}
	if none, err := parseSteering("\xef\xbb\xbf{}"); err != nil || none.notes != "" || none.pages != nil {
		t.Fatalf("an empty file, saved with a byte order mark: %+v, %v", none, err)
	}
}

// TestRunOutcomes: a run that fails, or panics, leaves the wiki failed rather
// than generating, so it can be tried again; a plan that is not JSON is one
// overview page; a wiki that is ready is left as it is.
func TestRunOutcomes(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, `INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`)
	run := func(model *ai.OpenAI) (string, error) {
		g := &Generator{DB: db, Catalog: &catalog.Catalog{DB: db, Queue: &jobs.Queue{DB: db}}, Retriever: &retrieval.Retriever{DB: db}, Chat: model}
		err := g.Run(ctx, 1, "abc", jobs.Progress{})
		var status string
		db.QueryRow(`SELECT wiki_status FROM snapshots WHERE id = 1`).Scan(&status)
		return status, err
	}
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer refused.Close()
	// The plan is refused; then, with no model at all, planning panics.
	for _, model := range []*ai.OpenAI{{BaseURL: refused.URL, ModelName: "m"}, nil} {
		if status, err := run(model); err == nil || status != catalog.WikiFailed {
			t.Fatalf("a failed run: %v, wiki %q", err, status)
		}
	}
	prose := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"I would rather not plan."}}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer prose.Close()
	if status, err := run(&ai.OpenAI{BaseURL: prose.URL, ModelName: "m"}); err != nil || status != catalog.WikiReady {
		t.Fatalf("a plan that is not JSON: %v, wiki %q", err, status)
	}
	if pages, err := (&Generator{DB: db}).pages(ctx, 1); err != nil || len(pages) != 1 || pages[0].title != "Overview" {
		t.Fatalf("pages: %+v %v", pages, err)
	}
	// Even a page found empty: a run that went on would write it.
	db.Exec(`UPDATE wiki_pages SET content = ''`)
	if status, err := run(nil); err != nil || status != catalog.WikiReady {
		t.Fatalf("a ready wiki: %v, wiki %q", err, status)
	}
	if pages, _ := (&Generator{DB: db}).pages(ctx, 1); len(pages) != 1 || pages[0].content != "" {
		t.Fatalf("a ready wiki was written again: %+v", pages)
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
	w, _ := Reader{DB: db}.Load(context.Background(), 1)
	if b, _ := json.Marshal(w.Pages); !strings.Contains(string(b), `"citations":[]`) || strings.Contains(string(b), "diagram") {
		t.Fatalf("a page without citations or imports, as JSON: %s", b)
	}
	db.Exec(`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (2, 'cmd/main.go', 'lib/x.go')`)
	if w, err := (Reader{DB: db}).Load(context.Background(), 1); err != nil || !strings.Contains(w.Pages[0].Diagram, `n0["cmd"]`) {
		t.Fatalf("the overview's diagram: %+v %v", w.Pages, err)
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
	many := `{"pages":[` + strings.Repeat(`{"title":"Page","query":"q"},`, maxPages) + `{"title":"One too many","query":"q"}]}`
	if got := parsePlan(many); len(got) != maxPages {
		t.Fatalf("%d pages planned, want at most %d", len(got), maxPages)
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
