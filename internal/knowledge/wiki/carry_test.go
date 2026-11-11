package wiki

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// basePages are the pages of carryDB's base wiki, each choosing and citing line 1 of its file.
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
// another repository's, each with one page. An empty file has no lines.
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
			exec(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (?, ?, 'go', ?, ?, ?)`, snap, path, content, len(content), min(len(content), 1))
			exec(`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES (?, ?, '', 1, 1, ?, ?)`, snap, path, content, path+content)
		}
	}
	for i, p := range basePages {
		var id int64
		if err := db.QueryRow(`INSERT INTO wiki_pages(snapshot_id, slug, title, query, position, content, files) VALUES (2, ?, ?, ?, ?, ?, ?) RETURNING id`,
			slug(i, p.title), p.title, p.query, i, aboutPage(p.title, p.file, "base"), `{"chosen":["`+p.file+`"],"placed":[],"units":["./"]}`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO wiki_citations(page_id, path, start_line, end_line) VALUES (?, ?, 1, 1)`, id, p.file)
	}
	return db
}

// TestRunCarriesPages: a new snapshot's wiki keeps the newest finished wiki's
// pages, their files following the change, rewriting only those whose files
// or cited lines changed or whose search finds a new file near what they
// cite; a page left with no file goes; kept citations follow their lines. It
// plans anew for another language, pages without files, or most files changed.
func TestRunCarriesPages(t *testing.T) {
	common := map[string]string{"a.go": "alpha one", "b.go": "beta", "d.go": "delta", "e.go": "epsilon", "f.go": "filler", "h.go": "core helper"} // a planned overview searches "core"
	files := func(changes map[string]string, drop ...string) map[string]string {
		out := maps.Clone(common)
		maps.Copy(out, changes)
		for _, p := range drop {
			delete(out, p)
		}
		return out
	}
	kept := func(title, file string) string { return aboutPage(title, file, "next") }
	moved := func(title, file string) string { return strings.ReplaceAll(kept(title, file), "1", "2") } // cites line 2
	planned := []string{"Planned", "Page ./"}
	for name, c := range map[string]struct {
		language   string
		base, next map[string]string
		old        bool // the base pages hold no files
		want       []string
		chosen     map[string]string // a page's first chosen file, when not its own
	}{
		// 3 of 7 files differ: Alpha cites the edited a.go; Delta's only file is gone, and so is it;
		// the new g.go goes on Beta, which is written again; Epsilon's search finds it, beside e.go.
		"carry": {"English", common, files(map[string]string{"a.go": "alpha two", "g.go": "gizmo"}, "d.go"), false,
			[]string{"Alpha", "Beta", "Epsilon"}, nil},
		// Beta's search finds the edited manifest, and Alpha's, the overview's, the README: the same chunks, which no page cites.
		"manifest and readme": {"English", files(map[string]string{"package.json": "beta deps one", "README.md": "Demo one"}),
			files(map[string]string{"package.json": "beta deps two", "README.md": "Demo two"}), false,
			[]string{"Alpha=" + kept("Alpha", "a.go"), "Beta=" + kept("Beta", "b.go"), "Delta=" + kept("Delta", "d.go"), "Epsilon=" + kept("Epsilon", "e.go")}, nil},
		// a.go grows below the line Alpha cites, b.go above Beta's: carried, Beta's citation moving with its line.
		"lines moved": {"English", common, files(map[string]string{"a.go": "alpha one\nmore", "b.go": "intro\nbeta"}), false,
			[]string{"Alpha=" + kept("Alpha", "a.go"), "Beta=" + moved("Beta", "b.go"), "Delta=" + kept("Delta", "d.go"), "Epsilon=" + kept("Epsilon", "e.go")}, nil},
		// web/ is a new small unit: its file goes on a page, written again; Epsilon's search finds it far from what it cites.
		"far new file": {"English", common, files(map[string]string{"web/w.go": "gizmo"}), false,
			[]string{"Alpha=" + kept("Alpha", "a.go"), "Beta", "Delta=" + kept("Delta", "d.go"), "Epsilon=" + kept("Epsilon", "e.go")}, nil},
		// a.go is now a2.go, as it was: Alpha keeps its place under the new path, and is written again as its citation's file is gone.
		"renamed": {"English", common, files(map[string]string{"a2.go": "alpha one"}, "a.go"), false,
			[]string{"Alpha", "Beta=" + kept("Beta", "b.go"), "Delta=" + kept("Delta", "d.go"), "Epsilon=" + kept("Epsilon", "e.go")}, map[string]string{"Alpha": "a2.go"}},
		// x/e.go and the new y/e.go are both empty: no rename, so y/e.go goes on a page, written again.
		"empty files": {"English", files(map[string]string{"x/e.go": ""}), files(map[string]string{"y/e.go": ""}), false,
			[]string{"Alpha=" + kept("Alpha", "a.go"), "Beta", "Delta=" + kept("Delta", "d.go"), "Epsilon=" + kept("Epsilon", "e.go")}, nil},
		"other language": {"Deutsch", common, files(map[string]string{"a.go": "alpha two"}), false, planned, nil},
		"before files":   {"English", common, files(map[string]string{"a.go": "alpha two"}), true, planned, nil},
		"wide change":    {"English", common, files(map[string]string{"a.go": "alpha two", "f.go": "filler two", "g.go": "gizmo"}, "d.go"), false, planned, nil},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := carryDB(t, c.language, c.base, c.next)
			if c.old {
				if _, err := db.Exec(`UPDATE wiki_pages SET files = NULL WHERE snapshot_id = 2`); err != nil {
					t.Fatal(err)
				}
			}
			model := &fakeModel{}
			g := runWiki(t, db, model, "next")
			pages, err := g.Store.Pages(ctx, 6)
			if err != nil {
				t.Fatal(err)
			}
			cites, _ := g.Store.Citations(ctx, 6)
			var got []string
			written := 0
			for _, p := range pages {
				if p.Content == "" || p.Files == nil {
					t.Fatalf("page %s left empty or without files", p.Title)
				}
				if strings.HasPrefix(p.Content, "Written.") {
					written++
					got = append(got, p.Title)
					continue
				}
				got = append(got, p.Title+"="+p.Content)
				i := slices.IndexFunc(basePages, func(b struct{ title, query, file string }) bool { return b.title == p.Title })
				if p.Query != basePages[i].query || len(cites[p.ID]) != 1 { // a kept page keeps its query and citation
					t.Errorf("page %s: query %q, citations %v", p.Title, p.Query, cites[p.ID])
				}
			}
			if !slices.Equal(got, c.want) || len(model.writes) != written {
				t.Fatalf("pages %q, %d written; want %q", got, len(model.writes), c.want)
			}
			for title, f := range c.chosen {
				if i := slices.IndexFunc(pages, func(p page) bool { return p.Title == title }); pages[i].Files.Chosen[0] != f {
					t.Errorf("%s chose %v, want %s", title, pages[i].Files.Chosen, f)
				}
			}
		})
	}
}

// TestRunFollowsImports: a page is written again when a file it cites
// imports a changed declaration, not when only a body changed; a change that
// adds a section's worth of code other code imports is planned anew, the plan
// seeing the imports between directories and the overview told the diagram
// is drawn.
func TestRunFollowsImports(t *testing.T) {
	common := map[string]string{"a.go": "alpha", "b.go": "beta", "d.go": "delta", "e.go": "epsilon", "h.go": "core helper", "tools/t.go": "tooling"} // a planned overview searches "core"
	next := maps.Clone(common)
	next["h.go"] = "core helper two"
	withDir := maps.Clone(common)
	withDir["tools/gen/x.go"] = "gizmo" // what the plan's page searches for
	for name, c := range map[string]struct {
		next          map[string]string
		before, after string // h.go's declaration in each snapshot
		edge          string // an import in the new snapshot
		plans, writes int
	}{
		"declaration": {next, "func H() {", "func H(n int) {", "b.go>h.go", 0, 1},
		"pathless":    {next, "func H() {", "func H(n int) {", "b.go>h.go", 0, 2}, // its citations leave out their file: asked again
		"uncited":     {next, "func H() {", "func H(n int) {", "b.go>h.go", 0, 2}, // it cites nothing: asked again
		"body":        {next, "func H() {", "func H() {", "b.go>h.go", 0, 0},
		"subsystem":   {withDir, "func H() {", "func H() {", "a.go>tools/gen/x.go", 1, 2},
	} {
		t.Run(name, func(t *testing.T) {
			db := carryDB(t, "English", common, c.next)
			src, dst, _ := strings.Cut(c.edge, ">")
			if _, err := db.Exec(`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (2, 'b.go', 'h.go'), (6, '` + src + `', '` + dst + `');
				INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature) VALUES
					(2, 'h.go', 'H', 'H', 'function', 1, 1, '` + c.before + `'), (6, 'h.go', 'H', 'H', 'function', 1, 1, '` + c.after + `'),
					(6, 'tools/gen/x.go', 'Gen', 'Gen', 'function', 1, 1, 'func Gen() {'), (6, 'tools/gen/x.go', 'Run', 'Run', 'function', 2, 2, 'func Run() {');
				UPDATE source_files SET line_count = 300 WHERE path = 'tools/gen/x.go'`); err != nil { // two subjects and 300 lines: a cap of 2
				t.Fatal(err)
			}
			model := &fakeModel{first: map[string]string{"pathless": "Written [1-1].", "uncited": "Written."}[name]}
			if cites, _ := runWiki(t, db, model, "next").Store.Citations(context.Background(), 6); len(model.plans) != c.plans || len(model.writes) != c.writes || model.first != "" && len(cites) != 4 {
				t.Fatalf("%d plans and %d pages written, citing %v; want %d and %d", len(model.plans), len(model.writes), cites, c.plans, c.writes)
			}
			if drawn := c.writes > 0 && strings.Contains(model.writes[0], drawnNote); c.plans > 0 && (!strings.Contains(model.plans[0], "./ → tools/gen/ (1)") || !drawn) ||
				c.plans == 0 && c.writes == 1 && drawn {
				t.Fatalf("plan requests %q\nwrite requests %q", model.plans, model.writes)
			}
		})
	}
}
