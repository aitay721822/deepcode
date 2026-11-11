package wiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/knowledge/wiki/outline/skeleton"
	"deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/knowledge/wiki/view"
	"deepcode/internal/platform/ai"
	aicontract "deepcode/internal/platform/ai/contract"
	"deepcode/internal/platform/jobs"
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	catalogstore "deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graph/store"
)

// generator is a generator of English wikis on db, asking model (nil: none).
func generator(db *sql.DB, model *ai.OpenAI) *Generator {
	graphs := &graph.Graph{Store: &graphstore.Store{DB: db}}
	return &Generator{Store: &store.Store{DB: db}, Graph: graphs, Chat: model, Language: "English",
		Catalog:   &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: &jobs.Queue{Store: &jobstore.Store{DB: db}}},
		Retriever: &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: graphs}}
}

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

// must runs q on db, failing t if it fails.
func must(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
}

// runWiki runs a generator of db, set by set, asking model for the wiki of commit sha.
func runWiki(t *testing.T, db *sql.DB, model http.Handler, sha string, set ...func(*Generator)) *Generator {
	t.Helper()
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)
	g := generator(db, &ai.OpenAI{BaseURL: srv.URL, ModelName: "m"})
	for _, s := range set {
		s(g)
	}
	if err := g.Run(context.Background(), 1, sha, jobs.Progress{}); err != nil {
		t.Fatal(err)
	}
	return g
}

// TestRunResumes: a retried job keeps the stored plan and the pages already
// written, and writes only those still empty (with nothing indexed, without
// the model); one resumed after DEEPCODE_WIKI_LANGUAGE changed drops the
// pages of the old language and plans again, so no wiki mixes two languages.
func TestRunResumes(t *testing.T) {
	for language, want := range map[string]string{"English": "Overview=Written before. Router", "Deutsch": "Planned"} {
		db := testDB(t,
			`INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status, wiki_language) VALUES (1, 1, 'abc', 'ready', 'failed', '`+language+`')`,
			`INSERT INTO wiki_pages(snapshot_id, slug, title, position, content) VALUES (1, '1-overview', 'Overview', 0, 'Written before.'), (1, '2-router', 'Router', 1, '')`)
		model := &fakeModel{}
		pages, err := runWiki(t, db, model, "abc").Store.Pages(context.Background(), 1)
		var got []string
		for _, p := range pages {
			if got = append(got, p.Title); p.Content == "Written before." {
				got[len(got)-1] += "=" + p.Content
			} else if p.Content == "" {
				t.Errorf("%s: page %s left empty", language, p.Title)
			}
		}
		var status, now string
		db.QueryRow(`SELECT wiki_status, wiki_language FROM snapshots WHERE id = 1`).Scan(&status, &now)
		if err != nil || strings.Join(got, " ") != want || len(model.plans) != map[bool]int{true: 1}[language != "English"] || status != catalog.WikiReady || now != "English" {
			t.Fatalf("%s: pages %q after %d plans, wiki %q in %q (%v)", language, got, len(model.plans), status, now, err)
		}
	}
}

// fakeModel answers a structured request with the least its schema asks for
// (fill), passed through alter when set (by the schema's name and how many
// of that name it was asked before, in asked), and a page's writer with
// draft's page, keeping the user message of every request; when first is
// set, a page's first draft is first, and its repair draft's page.
type fakeModel struct {
	mu            sync.Mutex
	plans, writes []string
	first         string
	alter         func(name string, n int, reply map[string]any)
	asked         map[string]int            // set with alter
	schemas       map[string]map[string]any // when set, each name's last schema
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []ai.Message
		Format   *struct {
			Schema struct {
				Name   string         `json:"name"`
				Schema map[string]any `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	reply := draft(body.Messages[1].Content)
	if s := body.Format; s != nil {
		f.plans = append(f.plans, body.Messages[1].Content)
		if f.schemas != nil {
			f.schemas[s.Schema.Name] = s.Schema.Schema
		}
		v := fill(s.Schema.Schema, nil).(map[string]any)
		if f.alter != nil {
			f.alter(s.Schema.Name, f.asked[s.Schema.Name], v)
			f.asked[s.Schema.Name]++
		}
		b, _ := json.Marshal(v)
		reply = string(b)
	} else {
		f.writes = append(f.writes, body.Messages[1].Content)
		if f.first != "" && len(body.Messages) == 2 {
			reply = f.first
		}
	}
	f.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
}

// fill is the least JSON schema s (at path at) asks for: each property, an
// array's fewest items (at least one), an enum's first value, an integer's
// least; a title "Page " + the key it is under ("Planned" for the overview),
// every other string "core".
func fill(s map[string]any, at []string) any {
	switch s["type"] {
	case "object":
		out := map[string]any{}
		props, _ := s["properties"].(map[string]any)
		for k, v := range props {
			out[k] = fill(v.(map[string]any), append(slices.Clone(at), k))
		}
		return out
	case "array":
		least, _ := s["minItems"].(float64)
		var out []any
		for i := range max(1, int(least)) {
			out = append(out, fill(s["items"].(map[string]any), append(slices.Clone(at), fmt.Sprint("#", i))))
		}
		return out
	case "integer":
		least, _ := s["minimum"].(float64)
		return int(least)
	}
	if e, ok := s["enum"].([]any); ok {
		return e[0]
	}
	if at[len(at)-1] != "title" { // a string is never the root
		return "core"
	} else if keys := slices.DeleteFunc(slices.Clone(at), func(k string) bool { return k[0] == '#' || k == "title" }); at[0] != "overview" {
		return "Page " + keys[len(keys)-1]
	}
	return "Planned"
}

// draft is a page citing the first <source> block request shows, as a writer
// is asked to, then each heading it lists, citing it too.
func draft(request string) string {
	out, cite := "Written.", ""
	if _, block, ok := strings.Cut(request, `<source path="`); ok {
		path, rest, _ := strings.Cut(block, `" lines="`)
		cite = " See [" + path + ":" + strings.SplitN(rest, `"`, 2)[0] + "]."
	}
	_, listed, _ := strings.Cut(request, "Its headings, in order:\n")
	for _, line := range strings.Split(listed, "\n") {
		if !strings.HasPrefix(line, "#") {
			break
		}
		out += cite + "\n\n" + line + "\n\nAbout it."
	}
	return out + cite
}

// TestRunPlansSections: a repository too big to plan flat gets the overview,
// each section under its head, each unit with pages of its own files (one the
// first reply left out repaired), a unit alone at the top, then the docs,
// the tests and the tooling, each of the last two one page at the top, as
// its share of four code pages is one; every code file is on a page; a
// section's page is written as its overview, a part as a part, and the nav
// reads each part's section. Under a ceiling the plan keeps to it and says
// what the structure wanted.
func TestRunPlansSections(t *testing.T) {
	stmts := []string{`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`, `INSERT INTO file_contents(hash, content) VALUES ('h', 'x')`,
		`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'api/a1.go', 'store/s1.go'), (1, 'api/a2.go', 'store/s2.go'), (1, 'web/w1.go', 'api/a1.go'), (1, 'docs/d1.md', 'ex/x.go')`}
	files := []string{"main.go", "go.mod", "docs/d1.md", "docs/d2.md", "api/a1.go", "api/a2.go", "api/a3.go", "api/a4.go", "store/s1.go", "store/s2.go",
		"store/s3.go", "store/s4.go", "web/w1.go", "web/w2.go", "web/w3.go", "web/w4.go", "api/a1_test.go", "store/s1_test.go", ".github/ci.yml", "ex/x.go"} // ex/: d1.md's
	for _, f := range files {
		lines := map[bool]int{false: 100, true: 600}[strings.HasPrefix(f, "docs/")]
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, '%s', 'go', 'h', 1, %d)`, f, lines))
		if strings.HasPrefix(f, "api/") { // two subjects each: api/'s cap is 3 (400 lines), the others' 1
			stmts = append(stmts, fmt.Sprintf(`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, '%[1]s', '%[1]sA', 'A', 'function', 1, 1), (1, '%[1]s', '%[1]sB', 'B', 'function', 2, 2)`, f))
		}
	}
	stmts = append(stmts, `INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'api/a1.go', 1, 1, 'core entry tests', 'c')`)
	db := testDB(t, stmts...)
	model := &fakeModel{asked: map[string]int{}, alter: func(name string, n int, reply map[string]any) {
		if name == "wiki_section" && n == 0 {
			delete(reply, "web/")
		}
	}}
	var planned Planning
	g := runWiki(t, db, model, "abc", func(g *Generator) { g.Parallel, g.Planned = 2, func(p Planning) { planned = p } })
	pages, _ := g.Store.Pages(context.Background(), 1)
	var got []string
	var plan []outline.Page
	for _, p := range pages {
		got = append(got, fmt.Sprintf("%s<%d", p.Title, p.Parent))
		plan = append(plan, outline.Page{Chosen: p.Files.Chosen, Placed: p.Files.Placed})
	}
	want := "Planned<-1 Page 1<-1 Page api/<1 Page store/<1 Page web/<1 Page p1<-1 Page pages<-1 Page 3<-1 Page 4<-1"
	counts := map[aicontract.Rule]aicontract.Count{"section.missing": {First: 1}}
	if strings.Join(got, " ") != want || len(model.writes) != len(pages) || planned.Requests != 4 || !reflect.DeepEqual(planned.Contracts, counts) || planned.Seconds <= 0 {
		t.Fatalf("pages %q, %d written; planned %+v", got, len(model.writes), planned)
	}
	if left := place.Unnamed(plan, files); len(left) > 0 || !slices.Equal(pages[5].Files.Placed, []string{"go.mod", "main.go"}) || !slices.Equal(pages[4].Files.Chosen, []string{"web/w1.go"}) ||
		!slices.Equal(pages[6].Files.Units, []string{"docs/d1.md", "docs/d2.md"}) || !slices.Equal(pages[6].Files.Placed, []string{"docs/d1.md", "ex/x.go", "docs/d2.md"}) {
		t.Fatalf("unplaced %v; top-level %+v; web %+v; docs %+v", left, pages[5].Files, pages[4].Files, pages[6].Files)
	}
	tests, tooling := [][]string{{"api/a1_test.go", "store/s1_test.go"}, {"api/" + place.TestsMark, "store/" + place.TestsMark}}, [][]string{{".github/ci.yml"}, {"tooling"}}
	if f, g := pages[7].Files, pages[8].Files; !reflect.DeepEqual([][]string{f.Placed, f.Units}, tests) || !reflect.DeepEqual([][]string{g.Placed, g.Units}, tooling) {
		t.Fatalf("tests %+v, tooling %+v", pages[7].Files, pages[8].Files)
	}
	if !slices.ContainsFunc(model.writes, func(u string) bool {
		return strings.Contains(u, "opens a section whose pages are: Page api/ | Page store/ | Page web/")
	}) {
		t.Fatalf("no page was told it opens the section: %q", model.writes)
	}
	// Sections are written before their pages, which share their sources ahead of what is their own.
	for i, u := range model.writes {
		if under := strings.Contains(u, `This page is part of the section "Page 1"`); under != (i >= 6) { // six at the top first
			t.Fatalf("write %d of %d is under a section: %v", i+1, len(model.writes), under)
		}
		if s := strings.Index(u, `Sources the pages of the section "Page 1" share`); strings.Contains(u, `the section "Page 1"`) && (s < 0 || s > strings.Index(u, "Write the page")) {
			t.Fatalf("write %d shares no sources ahead of its request:\n%s", i+1, u)
		}
	}
	must(t, db, `UPDATE snapshots SET wiki_status = 'ready'`)
	w, err := (view.Reader{Store: g.Store, Graph: g.Graph}).Load(context.Background(), 1)
	if err != nil || w.Pages[1].Parent != "" || w.Pages[2].Parent != w.Pages[1].Slug || !strings.Contains(w.Pages[0].Diagram, `["api"]`) {
		t.Fatalf("the nav: %+v %v", w.Pages[:3], err)
	}
	stored, _ := g.Store.Pages(context.Background(), 1)
	shown := 0
	for i, p := range stored { // each shown with its files, the chosen first
		var want []string
		if p.Files != nil {
			want = append(slices.Clone(p.Files.Chosen), p.Files.Placed...)
		}
		if shown += len(want); !slices.Equal(w.Pages[i].Files, want) {
			t.Fatalf("page %d shows files %q, want %q", i, w.Pages[i].Files, want)
		}
	}
	if shown == 0 {
		t.Fatal("no page shows its files")
	}
	var trimmed Planning
	g = runWiki(t, testDB(t, stmts...), &fakeModel{}, "abc", func(g *Generator) { g.MaxPages, g.Planned = 8, func(p Planning) { trimmed = p } })
	if pages, _ := g.Store.Pages(context.Background(), 1); trimmed.Ceiling == nil || *trimmed.Ceiling != (Ceiling{Limit: 8, Wanted: 12, Planned: len(pages)}) || len(pages) > 8 {
		t.Fatalf("under a ceiling of 8: %+v, %d pages", trimmed.Ceiling, len(pages))
	}
}

// TestRunGroupsAsides (follow-up 2): an aside of more units than a request
// names (asideUnits), a flat folder of 150 guides of a cap of 150, is asked
// in two levels: the section's request lists one group of them, which it
// gives three pages under "spans" (at most as many as its units); then its
// units are asked in two batches of 75 (aside.Spread), each putting its own
// on a page: the third page, given none, goes; every guide is on a page, as a
// unit and a file.
func TestRunGroupsAsides(t *testing.T) {
	stmts := []string{`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`, `INSERT INTO file_contents(hash, content) VALUES ('h', 'x')`}
	var docs []string
	for i := range 150 {
		docs = append(docs, fmt.Sprintf("docs/g%03d.md", i))
	}
	for _, f := range append([]string{"main.go", "go.mod", "api/a.go", "store/s.go"}, docs...) {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, '%s', 'go', 'h', 1, 600)`, f))
	}
	model := &fakeModel{schemas: map[string]map[string]any{}, asked: map[string]int{}, alter: func(name string, n int, reply map[string]any) {
		switch name {
		case "wiki_aside":
			reply["pages"] = []any{map[string]any{"title": "A", "terms": []any{}}, map[string]any{"title": "B", "terms": []any{}}, map[string]any{"title": "C", "terms": []any{}}}
			reply["spans"] = map[string]any{"docs/": []any{0, 1, 2}}
		case "wiki_spread":
			for k := range reply["assign"].(map[string]any) {
				reply["assign"].(map[string]any)[k] = n
			}
		}
	}}
	g := runWiki(t, testDB(t, stmts...), model, "abc", func(g *Generator) { g.MaxPages = 1000 })
	var listed, batches []int
	for _, m := range model.plans {
		if _, units, ok := strings.Cut(m, "documentation and guides. Its units:\n"); ok && strings.Contains(m, "The pages of these units' group") {
			batches = append(batches, strings.Count(units, "- \"docs/g"))
		} else if ok {
			listed = append(listed, strings.Count(units, "- \""))
		}
	}
	span := model.schemas["wiki_aside"]["properties"].(map[string]any)["spans"].(map[string]any)["properties"].(map[string]any)["docs/"].(map[string]any)
	if !slices.Equal(listed, []int{1}) || !slices.Equal(batches, []int{75, 75}) || span["maxItems"] != float64(150) {
		t.Fatalf("the docs requests: %v listed, batches %v, span %v", listed, batches, span)
	}
	stored, err := g.Store.Pages(context.Background(), 1)
	var units, placed []string
	var titles []string
	for _, p := range stored {
		if units, placed = append(units, p.Files.Units...), append(placed, p.Files.Placed...); slices.Contains(p.Files.Units, docs[0]) || slices.Contains(p.Files.Units, docs[149]) {
			titles = append(titles, fmt.Sprintf("%s:%d", p.Title, len(p.Files.Units)))
		}
	}
	if left := slices.DeleteFunc(slices.Clone(docs), func(f string) bool { return slices.Contains(units, f) && slices.Contains(placed, f) }); err != nil || len(left) > 0 ||
		!slices.Equal(titles, []string{"A:75", "B:75"}) || slices.ContainsFunc(stored, func(p store.Page) bool { return p.Title == "C" }) {
		t.Fatalf("guides on no page: %v; docs pages %v (%v)", left, titles, err)
	}
}

// TestRunOutcomes: a run that fails or panics leaves the wiki failed, to be
// tried again; so does a refused plan, and one not JSON after a repair
// (flat.json); a reply that reads but breaks its schema is fixed by code, an
// overview at the least; a ready wiki is left as it is.
func TestRunOutcomes(t *testing.T) {
	ctx, planned := context.Background(), Planning{}
	db := testDB(t, `INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`)
	g := generator(db, nil)
	g.Language, g.Planned = "", func(p Planning) { planned = p }
	run := func(model *ai.OpenAI) (string, error) {
		g.Chat = model
		status, err := "", g.Run(ctx, 1, "abc", jobs.Progress{})
		db.QueryRow(`SELECT wiki_status FROM snapshots WHERE id = 1`).Scan(&status)
		return status, err
	}
	if status, err := run(nil); err == nil || status != catalog.WikiFailed { // with no model at all, planning panics
		t.Fatalf("no model: %v, wiki %q", err, status)
	}
	all := aicontract.Count{First: 1, Repaired: 1, Residual: 1}
	for _, c := range []struct { // "" is refused; %q quotes the rest as JSON does
		reply string
		want  map[aicontract.Rule]aicontract.Count
	}{
		{"", nil},
		{"I would rather not plan.", map[aicontract.Rule]aicontract.Count{"flat.json": {First: 1, Repaired: 1}}},
		{`{"pages":[]}`, map[aicontract.Rule]aicontract.Count{"outline.title": all, "outline.label": all, "terms.missing": all}},
	} {
		reply, want := c.reply, c.want
		planned = Planning{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(map[bool]int{true: http.StatusBadRequest, false: http.StatusOK}[reply == ""])
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\n", reply)
		}))
		must(t, db, `DELETE FROM wiki_pages`)
		must(t, db, `UPDATE snapshots SET wiki_status = 'failed'`)
		status, err := run(&ai.OpenAI{BaseURL: srv.URL, ModelName: "m"})
		if schema := reply != "" && reply[0] != '{'; errors.Is(err, ai.ErrSchema) != schema || (status == catalog.WikiFailed) != (schema || reply == "") ||
			!maps.Equal(planned.Contracts, want) {
			t.Fatalf("%q: %v, wiki %q, planned %+v", reply, err, status, planned)
		}
		srv.Close()
	}
	if pages, err := g.Store.Pages(ctx, 1); err != nil || len(pages) != 1 || pages[0].Title != "Overview" {
		t.Fatalf("pages: %+v %v", pages, err)
	}
	must(t, db, `UPDATE wiki_pages SET content = ''`) // even a page found empty: a run that went on would write it
	if status, err := run(nil); err != nil || status != catalog.WikiReady {
		t.Fatalf("a ready wiki: %v, wiki %q", err, status)
	} else if pages, _ := g.Store.Pages(ctx, 1); len(pages) != 1 || pages[0].Content != "" {
		t.Fatalf("a ready wiki was written again: %+v", pages)
	}
}

// TestRunWritesSkeleton: a page is held to its skeleton, its chosen file's
// type. A first draft, fenced and titled, lacking the heading is cleaned and
// repaired, asked for that section alone, and the repair goes in after the
// intro; the page renders with it, cited, and Written tells it. Its placed
// file, with no heading, is listed last under the wiki's Other files label,
// linked, by its first three declarations, and so is its folder of tests,
// whose heading no source showed, by its path.
func TestRunWritesSkeleton(t *testing.T) {
	db := testDB(t, `INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status, wiki_language, wiki_labels) VALUES (1, 1, 'abc', 'ready', 'failed', 'English', '{"stack":"Stack","otherFiles":"More files"}')`,
		`INSERT INTO file_contents(hash, content) VALUES ('h', 'x')`,
		`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, 'a.go', 'go', 'h', 1, 9), (1, 'b.go', 'go', 'h', 1, 4)`,
		`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES (1, 'a.go', 'Router', 1, 9, 'func route()' || char(10) || 'type Router struct' || char(10, 51, 10, 52, 10, 53, 10, 54, 10, 55, 10, 56, 10, 57), 'c')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'a.go', 'route', 'route', 'function', 1, 1), (1, 'a.go', 'Router', 'Router', 'struct', 2, 9),
			(1, 'b.go', 'c', 'c', 'function', 2, 2), (1, 'b.go', 'B', 'B', 'struct', 1, 1), (1, 'b.go', 'e', 'e', 'function', 4, 4), (1, 'b.go', 'd', 'd', 'function', 3, 3)`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, query, position, content, files) VALUES (1, '1-overview', 'Overview', '', 0, 'Written before.', NULL),
			(1, '2-routing', 'Routing', 'router', 1, '', '{"chosen":["a.go"],"placed":["b.go"],"units":["./","e2e/ (tests)"]}')`)
	model := &fakeModel{first: "```markdown\n# Routing\nIntro.\n```"}
	var w Writing
	g := runWiki(t, db, model, "abc", func(g *Generator) { g.Written = func(x Writing) { w = x } })
	pages, err := g.Store.Pages(context.Background(), 1)
	if err != nil || len(pages) != 2 || !strings.HasPrefix(pages[1].Content, "Intro.\n\n## Router\n\nAbout it. See [a.go:1-9](") ||
		!strings.HasSuffix(pages[1].Content, "\n\n## More files\n\n- [b.go](https://example.com/o/r/blob/abc/b.go): `B`, `c`, `d`\n- `e2e/`") {
		t.Fatalf("%+v %v", pages, err)
	}
	if len(model.writes) != 2 || len(model.plans) != 0 || !strings.Contains(model.writes[0], "Its headings, in order:\n## Router\nIts files: a.go, b.go\n") ||
		!slices.Equal(w.Headings, []string{"Router"}) || len(w.Lacking) != 0 || !slices.Equal(w.Unsourced, []string{"e2e"}) || !w.Repaired ||
		w.Contracts["write.heading"] != (aicontract.Count{First: 1}) {
		t.Errorf("asked %q; written %+v", model.writes, w)
	}
}

// TestRunHeadListsNone: a section's head, held to no headings, lists none of
// its files under Other files, as its pages do theirs.
func TestRunHeadListsNone(t *testing.T) {
	db := testDB(t, `INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status, wiki_language) VALUES (1, 1, 'abc', 'ready', 'failed', 'English')`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'docs/a.md', 1, 9, 'guides text', 'c')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, query, position, parent, content, files) VALUES (1, '1-overview', 'Overview', '', 0, NULL, 'Written before.', NULL),
			(1, '2-guides', 'Guides', 'guides', 1, NULL, '', '{"chosen":[],"placed":["docs/a.md"],"units":["docs/a.md"]}'),
			(1, '3-guide', 'Guide', 'guides', 2, 1, '', '{"chosen":[],"placed":["docs/a.md"],"units":[]}')`)
	pages, err := runWiki(t, db, &fakeModel{}, "abc").Store.Pages(context.Background(), 1)
	if err != nil || len(pages) != 3 || strings.Contains(pages[1].Content, "## Other files") || !strings.HasSuffix(pages[2].Content, "## Other files\n\n- [docs/a.md](https://example.com/o/r/blob/abc/docs/a.md)") {
		t.Fatalf("%+v %v", pages, err)
	}
}

// TestRunCoversShownCode: code a guide includes joins its unit (place.Illustrations),
// so the guide's heading covers it: the page lists none of it under Other files.
func TestRunCoversShownCode(t *testing.T) {
	db := testDB(t, `INSERT INTO snapshots(id, repository_id, commit_sha, status, wiki_status, wiki_language) VALUES (1, 1, 'abc', 'ready', 'failed', 'English')`,
		`INSERT INTO file_contents(hash, content) VALUES ('g', '# Chapter One' || char(10) || 'Text.'), ('h', 'x')`,
		`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, 'src/ch01.md', 'markdown', 'g', 1, 2), (1, 'listings/a/Cargo.toml', 'toml', 'h', 1, 1), (1, 'listings/a/src/main.rs', 'rust', 'h', 1, 1)`,
		`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'src/ch01.md', 'listings/a/src/main.rs')`, `INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'src/ch01.md', 1, 2, 'Chapter One text', 'c')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, query, position, parent, content, files) VALUES (1, '1-overview', 'Overview', '', 0, NULL, 'Written before.', NULL), (1, '2-one', 'Chapter One', 'chapter', 1, NULL, '', '{"chosen":[],"placed":["listings/a/Cargo.toml","listings/a/src/main.rs","src/ch01.md"],"units":["src/ch01.md"]}')`)
	pages, err := runWiki(t, db, &fakeModel{}, "abc").Store.Pages(context.Background(), 1)
	if err != nil || len(pages) != 2 || !strings.Contains(pages[1].Content, "## Chapter One") || strings.Contains(pages[1].Content, "Other files") {
		t.Fatalf("%+v %v", pages, err)
	}
}

// TestSourced: a heading stays when a used hit of its file holds its line (any
// hit, for line 0), as does a fixed one with no line; one whose line no hit
// shows, or with a unit and no line, is left out, a level-2 one with those
// under it.
func TestSourced(t *testing.T) {
	at := func(p string, line int) retrieval.Anchor { return retrieval.Anchor{Path: p, Line: line} }
	used := []retrieval.Hit{{Path: "a.go", StartLine: 10, EndLine: 20}, {Path: "b.md", StartLine: 1, EndLine: 5}}
	kept, out := sourced(skeleton.Skeleton{{Name: "Stack", Level: 2}, {Name: "A", Level: 2, Unit: "a.go", Anchor: at("a.go", 15)},
		{Name: "A1", Level: 3, Unit: "a.go", Anchor: at("a.go", 0)}, {Name: "Gone", Level: 2, Unit: "a.go", Anchor: at("a.go", 30)},
		{Name: "Under", Level: 3, Unit: "a.go", Anchor: at("b.md", 2)}, {Name: "Bare", Level: 2, Unit: "d.go"}, {Name: "Lone", Level: 2, Anchor: at("e.go", 3)},
		{Name: "B", Level: 2, Unit: "b.md", Anchor: at("b.md", 5)}, {Name: "B1", Level: 3, Unit: "b.md", Anchor: at("b.md", 6)}}, used)
	var names []string
	for _, h := range kept {
		names = append(names, h.Name)
	}
	if strings.Join(names, " ") != "Stack A A1 B" || strings.Join(out, " ") != "Gone Under Bare Lone B1" {
		t.Errorf("kept %q, left out %q", names, out)
	}
}

// TestCleanSlug: a page loses reasoning, a wrapping fence and a title; its
// slug is its place and the ASCII words of its title.
func TestCleanSlug(t *testing.T) {
	for in, want := range map[string]string{"```markdown\nBody text\n```": "Body text", "# Title\nBody": "Body", "<think>x</think>\n## Section\nA": "## Section\nA"} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := []string{slug(0, "Overview"), slug(2, "Routing Tree & URL Matching"), slug(4, "概覽")}; !slices.Equal(got, []string{"1-overview", "3-routing-tree-url-matching", "5"}) {
		t.Errorf("slugs: %q", got)
	}
}
