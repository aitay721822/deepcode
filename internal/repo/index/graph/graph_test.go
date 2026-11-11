package graph

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/graph/store"
)

// TestResolve: Go imports name every non-test file of a package through the
// longest matching go.mod; relative JavaScript and TypeScript specifiers name
// a file as Node and TypeScript look one up; the rest name nothing.
func TestResolve(t *testing.T) {
	files := []file{
		{path: "main.go", lang: "go", imports: []string{"fmt", "example.com/m/pkg", "example.com/other/y", "example.com/m", "example.com/m/tools/v2/x"}},
		{path: "util.go", lang: "go"},
		{path: "pkg/a.go", lang: "go"},
		{path: "pkg/b.go", lang: "go"},
		{path: "pkg/a_test.go", lang: "go", imports: []string{"example.com/m/pkg"}},
		{path: "lib/y/y.go", lang: "go"},
		{path: "tools/x/x.go", lang: "go"},
		{path: "web/src/app.ts", lang: "typescript", imports: []string{
			"./util", "./util.ts", "../lib", "./esm.js", "./plain.js", "./style.css?raw", "react", "../../../outside", "./missing", "./app"}},
		{path: "web/src/react.ts", lang: "typescript"}, // a package's name is no path
		{path: "web/src/util.ts", lang: "typescript"},
		{path: "web/src/esm.ts", lang: "typescript"},
		{path: "web/src/plain.js", lang: "javascript"},
		{path: "web/lib/index.tsx", lang: "tsx", imports: []string{"."}},
		{path: "index.js", lang: "javascript", imports: []string{"./web/src/util?url"}},
	}
	// tools holds a module of its own, which wins over the root's for its paths.
	configs := map[string]string{"go.mod": "module example.com/m\n", "lib/go.mod": "module example.com/other\n",
		"tools/go.mod": "// tools\nmodule \"example.com/m/tools/v2\"\n\ngo 1.22\n"}
	var got []string
	// Each main is its nearest go.mod's entry; a test's main is none.
	for _, e := range resolve(files, configs, []string{"main.go", "tools/x/x.go", "pkg/a_test.go"}) {
		got = append(got, e.Src+" > "+e.Dst)
	}
	want := []string{
		"go.mod > main.go",
		"index.js > web/src/util.ts",
		"main.go > lib/y/y.go", "main.go > pkg/a.go", "main.go > pkg/b.go", "main.go > tools/x/x.go", "main.go > util.go",
		"pkg/a_test.go > pkg/a.go", "pkg/a_test.go > pkg/b.go",
		"tools/go.mod > tools/x/x.go",
		"web/src/app.ts > web/lib/index.tsx", "web/src/app.ts > web/src/esm.ts", "web/src/app.ts > web/src/plain.js", "web/src/app.ts > web/src/util.ts",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if m := moduleOf("// c\nmodule \"example.com/q\" // d\n\ngo 1.22\n"); m != "example.com/q" {
		t.Errorf("module %q", m)
	}
	if d, ok := goPackage("example.com/m/x", map[string]string{"b": "example.com/m", "a": "example.com/m"}); !ok || d != "a/x" {
		t.Errorf("two go.mod naming one module: %q", d)
	}
}

func open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func run(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// addFile stores a file of snapshot snap, its content's imports NULL as an
// older version left them.
func addFile(t *testing.T, db *sql.DB, snap int64, path, lang, content string) {
	t.Helper()
	hash := fmt.Sprintf("%x", content)
	run(t, db, `INSERT OR IGNORE INTO file_contents(hash, content) VALUES (?, ?)`, hash, content)
	run(t, db, `INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (?, ?, ?, ?, 1, 1)`, snap, path, lang, hash)
}

// TestLinkStale: the published snapshots an older version indexed have their
// files' imports read and their edges stored, once; a pruned one gets none.
func TestLinkStale(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	g := &Graph{Store: &store.Store{DB: db}}
	run(t, db, `INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r')`)
	run(t, db, `INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'a', 'ready'), (2, 1, 'b', 'pruned')`)
	for _, snap := range []int64{1, 2} {
		addFile(t, db, snap, "go.mod", "go.mod", "module example.com/m\n\ngo 1.22\n")
		addFile(t, db, snap, "main.go", "go", "package main\n\nimport \"example.com/m/pkg\"\n\nfunc main() { pkg.A() }\n")
		addFile(t, db, snap, "pkg/a.go", "go", "package pkg\n\nfunc A() {}\n")
		addFile(t, db, snap, "web/app.ts", "typescript", "import { u } from './util'\n")
		addFile(t, db, snap, "web/util.ts", "typescript", "export const u = 1\n")
		addFile(t, db, snap, "README.md", "markdown", "import x from './x'\n")
	}
	n, err := g.LinkStale(ctx)
	if err != nil || n != 1 {
		t.Fatalf("linked %d snapshots (%v), want 1", n, err)
	}
	edges := func(snap int64) string {
		var s sql.NullString
		db.QueryRow(`SELECT group_concat(src || '>' || dst, ' ') FROM (SELECT src, dst FROM file_edges WHERE snapshot_id = ? ORDER BY src, dst)`, snap).Scan(&s)
		return s.String
	}
	if got := edges(1); got != "main.go>pkg/a.go web/app.ts>web/util.ts" {
		t.Fatalf("edges %q", got)
	}
	var unread int
	db.QueryRow(`SELECT count(*) FROM file_contents WHERE imports IS NULL`).Scan(&unread)
	if unread != 2 { // go.mod and README.md: no grammar reads them
		t.Fatalf("%d contents unread, want 2", unread)
	}
	if n, err := g.LinkStale(ctx); err != nil || n != 0 {
		t.Fatalf("linked again: %d (%v)", n, err)
	}
	if n, err := g.Link(ctx, 2); err != nil || n != 0 || edges(2) != "" {
		t.Fatalf("a pruned snapshot got %d edges (%v)", n, err)
	}
	// More edges than one transaction writes all land, and are all replaced.
	run(t, db, `INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (3, 1, 'c', 'ready')`)
	many := make([]edge, storeBatch+1)
	for i := range many {
		many[i] = edge{Src: fmt.Sprintf("s%05d.go", i), Dst: "d.go"}
	}
	count := func() (n int) {
		db.QueryRow(`SELECT count(*) FROM file_edges WHERE snapshot_id = 3`).Scan(&n)
		return n
	}
	if n, err := g.save(ctx, 3, many); err != nil || n != len(many) || count() != len(many) {
		t.Fatalf("stored %d, %d in the table (%v)", n, count(), err)
	}
	if n, err := g.save(ctx, 3, many[:2]); err != nil || n != 2 || count() != 2 {
		t.Fatalf("stored again %d, %d in the table (%v)", n, count(), err)
	}
	if n, err := g.save(ctx, 99, many[:2]); err != nil || n != 0 {
		t.Fatalf("a snapshot deleted meanwhile: %d (%v)", n, err)
	}

	// What retrieval and the wiki read.
	if got, err := g.Dependencies(ctx, 1, []string{"main.go", "web/util.ts"}, 10); err != nil || !reflect.DeepEqual(got, []string{"pkg/a.go"}) {
		t.Errorf("what main.go and web/util.ts import: %v %v", got, err)
	}
	if got, _ := g.Dependencies(ctx, 1, []string{"pkg/a.go"}, 10); len(got) != 0 {
		t.Errorf("pkg/a.go imports nothing, its importers aside: %v", got)
	}
	if got, _ := g.Dependencies(ctx, 1, []string{"main.go", "pkg/a.go"}, 10); len(got) != 0 {
		t.Errorf("the files asked about are not their own dependencies: %v", got)
	}
	if got, err := g.DirEdges(ctx, 1, 1); err != nil || !reflect.DeepEqual(got, []DirEdge{{".", "pkg", 1}}) {
		t.Errorf("directory edges: %+v %v", got, err)
	}
	if got, err := g.Diagram(ctx, 1); err != nil || got != "flowchart TD\n  n0[\"(root)\"]\n  n1[\"pkg\"]\n  n0 --> n1\n" {
		t.Errorf("diagram %q %v", got, err)
	}
	run(t, db, `INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'x.go', 'b.go'), (1, 'y.go', 'b.go'), (1, 'x.go', 'a.go')`)
	if got, _ := g.Dependencies(ctx, 1, []string{"x.go", "y.go"}, 10); !reflect.DeepEqual(got, []string{"b.go", "a.go"}) {
		t.Errorf("what both import comes first: %v", got)
	}
}

// TestImpact: the importers of a changed file, changed ones too (gen.go),
// reach the impact when its declarations changed, not when only its bodies did
// or it only gained one, and still when the change removed it; a directory the
// change adds counts as new once a file outside it imports it.
func TestImpact(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	g := &Graph{Store: &store.Store{DB: db}}
	run(t, db, `INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r')`)
	run(t, db, `INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'a', 'ready'), (2, 1, 'b', 'ready'), (3, 1, 'c', 'ready')`)
	for _, snap := range []int64{1, 2, 3} {
		addFile(t, db, snap, "go.mod", "go.mod", "module m\n")
		addFile(t, db, snap, "main.go", "go", "package main\n\nimport (\n\t\"m/internal/pkg\"\n\t\"m/internal/tools/gen\"\n)\n")
	}
	for _, snap := range []int64{1, 2} {
		addFile(t, db, snap, "internal/pkg/a.go", "go", fmt.Sprintf("package pkg\n\nfunc A() {} // %d\n", snap))
	}
	addFile(t, db, 2, "internal/tools/gen/gen.go", "go", "package gen\n\nimport \"m/internal/pkg\"\n")
	// Snapshot 3 removes internal/pkg and adds internal/extra, which only its own code imports.
	addFile(t, db, 3, "internal/extra/x/x.go", "go", "package x\n\nimport \"m/internal/extra/y\"\n")
	addFile(t, db, 3, "internal/extra/y/y.go", "go", "package y\n")
	addFile(t, db, 3, "test/e2e.go", "go", "package e2e\n\nimport \"m/internal/extra/y\"\n") // a test's import wires nothing in
	for _, snap := range []int64{1, 2, 3} {
		if _, err := g.Link(ctx, snap); err != nil {
			t.Fatal(err)
		}
	}
	changed := map[string]bool{"internal/pkg/a.go": true, "internal/tools/gen/gen.go": true}
	declare := func(snap int64, sig string) { // B, declared before A in the comparison's order, never changes
		run(t, db, `DELETE FROM symbols WHERE snapshot_id = ?`, snap)
		run(t, db, `INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature)
			VALUES (?, 'internal/pkg/a.go', 'A', 'A', 'function', 3, 3, ?), (?, 'internal/pkg/a.go', 'B', 'B', 'const', 4, 4, 'const B = 1')`, snap, sig, snap)
	}
	declare(1, "func A() {")
	declare(2, "func A() {")
	if got, err := g.Impact(ctx, 1, 2, map[string]bool{"internal/pkg/a.go": true}); err != nil || len(got) != 0 {
		t.Errorf("a body's change reached %v (%v)", got, err)
	}
	run(t, db, `INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature)
		VALUES (2, 'internal/pkg/a.go', 'C', 'C', 'function', 5, 5, 'func C() {')`)
	if got, err := g.Impact(ctx, 1, 2, map[string]bool{"internal/pkg/a.go": true}); err != nil || len(got) != 0 {
		t.Errorf("a new declaration reached %v (%v)", got, err)
	}
	declare(2, "func A(n int) {")
	if got, err := g.Impact(ctx, 1, 2, changed); err != nil || !reflect.DeepEqual(got, map[string]bool{"main.go": true, "internal/tools/gen/gen.go": true}) {
		t.Errorf("a changed signature reached %v (%v), want main.go and gen.go", got, err)
	}
	if got, err := g.Impact(ctx, 1, 3, map[string]bool{"internal/pkg/a.go": true}); err != nil || !reflect.DeepEqual(got, map[string]bool{"main.go": true}) {
		t.Errorf("a removed file reached %v (%v), want main.go", got, err)
	}
	// The diagram cuts directories as deep as fit.
	if d, err := g.Diagram(ctx, 2); err != nil || !strings.Contains(d, `["internal/tools/gen"]`) {
		t.Errorf("diagram %q %v", d, err)
	}
}

// TestDiagram: the directories are cut as deep as keeps them at most
// maxNodes; past that the busiest arrows that fit are drawn, at most
// maxEdges, and a label cannot end its quotes.
func TestDiagram(t *testing.T) {
	var edges []edge
	for i := range 20 {
		edges = append(edges, edge{Src: fmt.Sprintf("a/b%02d/c/x.go", i), Dst: "z/y.go"})
	}
	if got := draw(group(edges, 1)); got != "flowchart TD\n  n0[\"a\"]\n  n1[\"z\"]\n  n0 --> n1\n" {
		t.Errorf("one segment: %q", got)
	}
	d := draw(group(edges, 2)) // a/b00 only leads on to c: one level
	if strings.Count(d, "-->") != maxNodes-1 || !strings.Contains(d, `n0["a/b00/c"]`) {
		t.Errorf("trimmed to %d nodes:\n%s", maxNodes, d)
	}
	// Java's folders above its packages are the trunk, no level, and the
	// labels skip what all share.
	const pkg = "src/main/java/org/acme/shop/"
	java := []edge{{Src: pkg + "owner/Owners.java", Dst: pkg + "model/Person.java"}, {Src: pkg + "vet/Vets.java", Dst: pkg + "model/Entity.java"},
		{Src: "src/test/java/org/acme/shop/owner/OwnersTests.java", Dst: pkg + "owner/Owners.java"}, {Src: pkg + "App.java", Dst: pkg + "vet/Vets.java"}}
	if got := draw(group(java, 1)); got != "flowchart TD\n  n0[\"shop\"]\n  n1[\"shop/vet\"]\n  n2[\"shop/owner\"]\n  n3[\"shop/model\"]\n  n0 --> n1\n  n2 --> n3\n  n1 --> n3\n" {
		t.Errorf("packages: %q", got)
	}
	// So is src/ when all is in it.
	if got := group([]edge{{Src: "src/a/y.go", Dst: "src/b/z.go"}}, 1); !reflect.DeepEqual(got, []DirEdge{{"src/a", "src/b", 1}}) {
		t.Errorf("under src: %+v", got)
	}
	// A label shows a long run of folded folders by its first and last.
	const g, x = "gson/src/main/java/com/google/gson", "extras/src/main/java/com/google/gson/typeadapters"
	modules := levels([]edge{{Src: g + "/internal/A.java", Dst: g + "/B.java"}, {Src: x + "/C.java", Dst: g + "/B.java"}})
	if a, b := modules.short(g+"/internal"), modules.short(x); a != "gson/…/gson/internal" || b != "extras/…/typeadapters" {
		t.Errorf("short: %q %q", a, b)
	}
	if got := draw([]DirEdge{{From: `x"]`, To: "<y#>`", Count: 1}}); !strings.Contains(got, `n0["x#quot;]"]`) || !strings.Contains(got, `n1["#lt;y#35;#gt;#96;"]`) {
		t.Errorf("escaped: %q", got)
	}
	// Tests, examples and docs show how the code is used, not how its parts depend on each other.
	used := []edge{{Src: "_examples/rest/main.go", Dst: "mux.go"}, {Src: "middleware/logger_test.go", Dst: "mux.go"}, {Src: "web/src/a.test.ts", Dst: "web/lib/x.ts"},
		{Src: "tests/test_x.go", Dst: "pkg/y.go"}, {Src: "docs/demo/a.ts", Dst: "web/lib/x.ts"}, {Src: "middleware/logger.go", Dst: "mux.go"}}
	if got := group(used, 1); !reflect.DeepEqual(got, []DirEdge{{"middleware", ".", 1}}) {
		t.Errorf("grouped with tests and examples aside: %+v", got)
	}
	if got := group([]edge{{Src: "a/x.go", Dst: "b/y.go"}, {Src: "c/x.go", Dst: "b/y.go"}, {Src: "c/z.go", Dst: "b/w.go"}}, 1); !reflect.DeepEqual(got, []DirEdge{{"c", "b", 2}, {"a", "b", 1}}) {
		t.Errorf("busiest first: %+v", got)
	}
	var full []edge // six directories all importing each other: 30 arrows
	for i := range 6 {
		for j := range 6 {
			if i != j {
				full = append(full, edge{Src: fmt.Sprintf("d%d/x.go", i), Dst: fmt.Sprintf("d%d/y.go", j)})
			}
		}
	}
	if n := strings.Count(draw(group(full, 1)), "-->"); n != maxEdges {
		t.Errorf("%d arrows, want %d", n, maxEdges)
	}
	if draw(nil) != "" {
		t.Error("no edges, no diagram")
	}
}
