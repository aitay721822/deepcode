package retrieval

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graph/store"
)

func TestFuse(t *testing.T) {
	got := fuse([]int64{1, 2, 3}, []int64{3, 4}, []int64{3, 2})
	if want := []int64{3, 2, 1, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Two lists agreeing on a chunk outweigh one list's first place; a tie goes to the lower id.
	if got, want := fuse([]int64{1, 5, 6, 2}, []int64{7, 8, 9, 2}), []int64{2, 1, 7, 5, 8, 6, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFTSQuery(t *testing.T) {
	if got, want := ftsQuery("How does the getUserName function work in auth_service?"), `"getusername"* OR "auth_service"* OR "user"* OR "name"*`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	// At most 16 terms, the question's own words before the words of its identifiers.
	long := ftsQuery("How do getUserToken, refreshAccessToken, validateSessionCookie, parseAuthHeader and loadConfigFile interact with the rate limiter?")
	if strings.Count(long, " OR ") != 15 || !strings.Contains(long, `"limiter"*`) {
		t.Fatalf("a long question: %s", long)
	}
	if got := ftsQuery("講解vscode的幾個組件"); got != `"vscode"*` {
		t.Fatalf("got %s", got)
	}
}

// seeded opens a scratch database that holds repository 1 at snapshot 1, then
// runs the test's own inserts.
func seeded(t *testing.T, inserts ...string) *sql.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range append([]string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
	}, inserts...) {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// retriever is a retriever on db, without vector search.
func retriever(db *sql.DB) *Retriever {
	return &Retriever{Store: &store.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}}}
}

// TestReadme: the first two chunks of the top-level README, in order; a
// file in a folder named readme is not the repository's README.
func TestReadme(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'README.md', 40, 60, 'later', 'h1'), (1, 'README.md', 1, 20, 'intro', 'h2'), (1, 'README.md', 21, 39, 'usage', 'h3'),
			(1, 'readme/guide.md', 1, 9, 'nested', 'h4'), (1, 'main.go', 1, 9, 'code', 'h5')`,
	)
	hits, err := retriever(db).Readme(context.Background(), 1)
	var got []string
	for _, h := range hits {
		got = append(got, h.Content)
	}
	if err != nil || !reflect.DeepEqual(got, []string{"intro", "usage"}) {
		t.Fatalf("readme: %v %v", got, err)
	}
}

// TestFTSRanksCodeFirst: stemming lets "subscribers" find subscribe, and the
// implementation outranks a test that repeats the words more often.
func TestFTSRanksCodeFirst(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
			VALUES (1, 'tests/store.test.ts', 1, 9, 'subscribe(a); subscribe(b); setState(1); expect(a).toHaveBeenCalled()', 'h1')`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
			VALUES (1, 'src/store.ts', 1, 9, 'const setState = (next) => { state = next; listeners.forEach((l) => l(state)) }
			const subscribe = (listener) => { listeners.add(listener) }', 'h2')`,
	)
	ids, err := retriever(db).fts(context.Background(), 1, "How does setState notify subscribers?", nil, ftsLimit)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, id := range ids {
		var p string
		db.QueryRow(`SELECT path FROM chunks WHERE id = ?`, id).Scan(&p)
		paths = append(paths, p)
	}
	if want := []string{"src/store.ts", "tests/store.test.ts"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
	if ids, _ := retriever(db).fts(context.Background(), 1, "subscribers", nil, ftsLimit); len(ids) != 2 {
		t.Fatalf("\"subscribers\" matched %d chunks, want both (stemmed to subscrib)", len(ids))
	}
}

// TestFTSFindsWordsOfIdentifiers: a question's word finds the compound
// identifiers made of it through their terms, as well as the same word in
// prose, and a compound identifier in a question finds others that share its
// words.
func TestFTSFindsWordsOfIdentifiers(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash, terms) VALUES
			(1, 'auth/session.go', 1, 9, 'func refreshUserToken() {}', 'h1', 'refresh user token'),
			(1, 'auth/check.go', 1, 9, 'the token', 'h2', ''),
			(1, 'cart/cart.go', 1, 9, 'func checkout() {}', 'h3', '')`,
	)
	for _, q := range []string{"Where is the token renewed?", "Who calls userToken?"} {
		ids, err := retriever(db).fts(context.Background(), 1, q, nil, ftsLimit)
		slices.Sort(ids)
		if err != nil || !reflect.DeepEqual(ids, []int64{1, 2}) {
			t.Errorf("%q found chunks %v %v, want refreshUserToken's (1) and the prose (2)", q, ids, err)
		}
	}
}

// TestSymbolSearch: % and _ in a query match only themselves, and names that
// start with the query come before shorter ones that merely contain it.
func TestSymbolSearch(t *testing.T) {
	db := seeded(t,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'a.go', 'xa_', 'xa_', 'function', 1, 1), (1, 'a.go', 'a_bc', 'a_bc', 'function', 2, 2),
			(1, 'a.go', 'a_', 'a_', 'function', 3, 3), (1, 'a.go', 'axb', 'axb', 'function', 4, 4)`,
	)
	hits, err := retriever(db).Symbols(context.Background(), 1, "a_", 10)
	var names []string
	for _, h := range hits {
		names = append(names, h.Name)
	}
	if err != nil || strings.Join(names, " ") != "a_ a_bc xa_" {
		t.Fatalf("%v %v", names, err)
	}
	if hits, _ := retriever(db).Symbols(context.Background(), 1, "a_", 2); len(hits) != 2 {
		t.Fatalf("a limit of 2 gave %d", len(hits))
	}
}

// TestSymbolChunks: names in a query find the chunks that define them, in any
// case and longest name first; only the first 32 names are looked up.
func TestSymbolChunks(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'main.go', 1, 9, 'func Target() {}', 'h1'), (1, 'handler.go', 1, 9, 'func TargetHandler() {}', 'h2')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'main.go', 'Target', 'Target', 'function', 1, 1), (1, 'handler.go', 'TargetHandler', 'TargetHandler', 'function', 1, 1)`,
	)
	var others []string
	for i := range 32 {
		others = append(others, fmt.Sprintf("Other%d", i))
	}
	r := retriever(db)
	if last, err := r.symbolChunks(context.Background(), 1, strings.Join(others[:31], " ")+" Target"); err != nil || len(last) != 1 {
		t.Fatalf("the 32nd name: %v %v", last, err)
	}
	if late, _ := r.symbolChunks(context.Background(), 1, strings.Join(others, " ")+" Target"); len(late) != 0 {
		t.Fatalf("the 33rd name was looked up: %v", late)
	}
	if ids, err := r.symbolChunks(context.Background(), 1, "target or targethandler"); err != nil || !reflect.DeepEqual(ids, []int64{2, 1}) {
		t.Fatalf("got chunks %v %v, want TargetHandler's (2) before Target's (1)", ids, err)
	}
}

// TestSymbolChunksJoined: adjacent words name a method or function written as
// one word, not across a stopword, and never a type: a question's nouns.
func TestSymbolChunksJoined(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'context.go', 1, 9, 'func (c *Context) RoutePattern() string', 'h1'), (1, 'item.cs', 1, 9, 'class CatalogItem {}', 'h2')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'context.go', 'RoutePattern', 'Context.RoutePattern', 'method', 1, 1), (1, 'item.cs', 'CatalogItem', 'CatalogItem', 'class', 1, 1)`,
	)
	r := retriever(db)
	for q, want := range map[string][]int64{
		"How can I get the matched route pattern?":        {1},
		"Which route is the pattern of this request?":     nil, // a stopword between
		"What happens when a catalog item price changes?": nil, // a type
	} {
		if ids, err := r.symbolChunks(context.Background(), 1, q); err != nil || !reflect.DeepEqual(ids, want) {
			t.Errorf("%q: got %v %v, want %v", q, ids, err, want)
		}
	}
	if got := joined("matched route pattern of it"); !reflect.DeepEqual(got, []string{"matchedroute", "matchedroutepattern", "routepattern"}) {
		t.Errorf("joined: %q", got)
	}
}

// TestSearchImports: a chunk of a file the top hits import takes a place of
// the last nearSlots, behind the best hits, though better matches elsewhere
// would fill the limit; without the imports it is left out.
func TestSearchImports(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES
			(1, 'a.go', 'route', 1, 9, 'func route() {}', 'h0'),
			(1, 'f1.go', '', 1, 9, 'route route route', 'h1'), (1, 'f2.go', '', 1, 9, 'route route route', 'h2'),
			(1, 'f3.go', '', 1, 9, 'route route route', 'h3'), (1, 'f4.go', '', 1, 9, 'route route route', 'h4'),
			(1, 'f5.go', '', 1, 9, 'route route route', 'h5'), (1, 'c.go', '', 1, 9, 'route route', 'h6'),
			(1, 'b.go', '', 1, 9, 'route', 'h7')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'a.go', 'route', 'route', 'function', 1, 1)`,
		// Not the top hit's: those ranked after it.
		`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'f1.go', 'b.go'), (1, 'f2.go', 'b.go'), (1, 'f3.go', 'b.go'), (1, 'f4.go', 'b.go'), (1, 'f5.go', 'b.go')`,
	)
	r := retriever(db)
	paths := func() ([]string, int) {
		res, err := r.Search(context.Background(), 1, "route", 4)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range res.Hits {
			out = append(out, h.Path)
		}
		return out, res.Graph
	}
	if got, n := paths(); n != 1 || len(got) != 4 || got[0] != "a.go" || got[2] != "b.go" || slices.Contains(got, "c.go") {
		t.Fatalf("with the import: %v (%d from imported files), want b.go third", got, n)
	}
	if _, err := db.Exec(`DELETE FROM file_edges`); err != nil {
		t.Fatal(err)
	}
	if got, n := paths(); n != 0 || len(got) != 4 || slices.Contains(got, "b.go") {
		t.Fatalf("without it: %v (%d from imported files), want no b.go", got, n)
	}
	// The imported code fills only what the best hits leave, the best one always kept, and never twice.
	for _, c := range []struct {
		ranked, near []int64
		limit        int
		want         []int64
		took         int // places of near
	}{
		{[]int64{1, 2, 3, 4, 5}, []int64{9, 8, 7}, 4, []int64{1, 2, 9, 8}, 2},
		{[]int64{1, 2, 3, 4, 5}, []int64{2, 9}, 4, []int64{1, 2, 9, 3}, 1},
		{[]int64{1, 2}, []int64{9}, 4, []int64{1, 2, 9}, 1},
		{[]int64{1, 2}, []int64{9, 8, 7}, 6, []int64{1, 2, 9, 8}, 2},
		{[]int64{1, 2, 3}, nil, 2, []int64{1, 2}, 0},
		{[]int64{1, 2, 3}, []int64{9, 8}, 2, []int64{1, 9}, 1},
		{[]int64{1, 2, 3}, []int64{9, 8}, 1, []int64{1}, 0},
	} {
		if got, took := supplement(c.ranked, c.near, c.limit); !slices.Equal(got, c.want) || took != c.took {
			t.Errorf("supplement(%v, %v, %d) = %v, %d; want %v, %d", c.ranked, c.near, c.limit, got, took, c.want, c.took)
		}
	}
}

// TestUsers: the chunks that use a name, from the files importing a file that
// defines it (either of two that do), the files using more of the names
// first; one chunk a file, tests aside, and a name only as the code spells it.
func TestUsers(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'pay/event.go', 1, 9, 'type PaymentDone struct{}', 'h1'), (1, 'order/event.go', 1, 9, 'type PaymentDone struct{}', 'h2'),
			(1, 'order/handle.go', 1, 9, 'func Handle(e PaymentDone) { SetPaid() }', 'h3'), (1, 'order/handle.go', 10, 19, 'PaymentDone again', 'h4'),
			(1, 'order/wire.go', 1, 9, 'bus.On(PaymentDone{}); bus.On(Shipped{})', 'h5'), (1, 'order/other.go', 1, 9, 'imports, uses neither', 'h6'),
			(1, 'order/handle_test.go', 1, 9, 'Handle(PaymentDone{})', 'h7'), (1, 'ship/event.go', 1, 9, 'type Shipped struct{}', 'h8')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'pay/event.go', 'PaymentDone', 'pay.PaymentDone', 'struct', 1, 1), (1, 'order/event.go', 'PaymentDone', 'order.PaymentDone', 'struct', 1, 1),
			(1, 'ship/event.go', 'Shipped', 'ship.Shipped', 'struct', 1, 1)`,
		`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'order/handle.go', 'order/event.go'), (1, 'order/wire.go', 'pay/event.go'),
			(1, 'order/wire.go', 'ship/event.go'), (1, 'order/other.go', 'order/event.go'), (1, 'order/handle_test.go', 'order/event.go')`,
	)
	r := retriever(db)
	for _, c := range []struct {
		names []string
		limit int
		want  []string
	}{
		{[]string{"PaymentDone", "Shipped", "Missing"}, 5, []string{"order/wire.go:1", "order/handle.go:1"}},
		{[]string{"PaymentDone"}, 1, []string{"order/handle.go:1"}},
		{[]string{"paymentdone"}, 5, nil},
		{nil, 5, nil},
	} {
		hits, err := r.Users(context.Background(), 1, c.names, c.limit)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, h := range hits {
			got = append(got, fmt.Sprintf("%s:%d", h.Path, h.StartLine))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("Users(%q, %d) = %q, want %q", c.names, c.limit, got, c.want)
		}
	}
}

// TestSearchNamedFiles: a file the query names by path, file name, folder or
// glob is ranked among the hits, though its words lose to every other file's;
// a name for more files than maxFiles names none.
func TestSearchNamedFiles(t *testing.T) {
	for query, want := range map[string][]string{
		"Focus: (src/app/Host.cs), ./README.md, tests/*.rs; examples/ App.java. Not .NET, 0.25, e.g. or res.send, see https://x.io/a.go, [app/[slug]/page.tsx] and app/(group) twice: src/app/Host.cs": {
			"src/app/Host.cs", "README.md", "tests/*.rs", "examples/", "App.java", "app/[slug]/page.tsx", "app/(group)"},
		"重點：src/a.cs、src/b.cs（見 lib/c.go:42）與「d/e.ts」":                          {"src/a.cs", "src/b.cs", "lib/c.go", "d/e.ts"},
		`**src/a.ts** and src\b\c.go#L10-L20 or (group)/page.tsx. (See x/y.go)`: {"src/a.ts", "src/b/c.go", "(group)/page.tsx", "x/y.go"},
		"(group)/(admin)/page.tsx, src/a.go:42:7, path=src/x.go|src/y.go 請看 App.java？ **src/b.go**. **c/d.go**:": {
			"(group)/(admin)/page.tsx", "src/a.go", "src/x.go", "src/y.go", "App.java", "src/b.go", "c/d.go"},
	} {
		if got := fileNames(query); !slices.Equal(got, want) {
			t.Errorf("fileNames(%q) = %q, want %q", query, got, want)
		}
	}
	for in, want := range map[string]string{"(": "", "()": "", "[[]]": "", "(a]": "a", "(group)/(admin)": "(group)/(admin)", "((a/b))": "a/b", "a/b)": "a/b"} {
		if got := unwrap(in); got != want {
			t.Errorf("unwrap(%q) = %q, want %q", in, got, want)
		}
	}
	inserts := []string{`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
		(1, 'src/app/Host.cs', 1, 9, 'var b = Builder.Create()', 'h0'), (1, 'app/[slug]/page.tsx', 1, 9, 'page', 'hp'),
		(1, 'lib/App.java', 1, 9, 'class App', 'ha'), (1, 'tests/x/a.rs', 1, 9, 'fn t()', 'ht'),
		(1, 'README.md', 1, 9, 'intro', 'hr'), (1, 'A/README.md', 1, 9, 'a', 'hra'), (1, 'B/README.md', 1, 9, 'b', 'hrb'),
		(1, 'lib/Late.go', 20, 29, 'func later()', 'hl2'), (1, 'lib/Late.go', 10, 19, 'func late()', 'hl1')`}
	for i := range 6 {
		inserts = append(inserts, fmt.Sprintf(`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
			VALUES (1, 'src/s%d/Extensions.cs', 1, 9, 'host app extensions host app', 'e%d')`, i, i))
	}
	var late []string // eight files, each named past the query's first 16 words
	for i := range 8 {
		late = append(late, fmt.Sprintf("far/L%d.go", i))
		inserts = append(inserts, fmt.Sprintf(`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
			VALUES (1, 'far/L%d.go', 1, 9, 'func f()', 'l%d')`, i, i))
	}
	db := seeded(t, inserts...)
	r := retriever(db)
	search := func(q string) ([]string, int) {
		res, err := r.Search(context.Background(), 1, q, 4)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range res.Hits {
			out = append(out, h.Path)
		}
		return out, res.Named
	}
	if got, n := search("Host app extensions"); slices.Contains(got, "src/app/Host.cs") || n != 0 {
		t.Fatalf("unnamed, Host.cs ranks among %q (%d named)", got, n)
	}
	for _, name := range []string{"src/app/Host.cs", "Host.cs", "src/app"} {
		if got, n := search("Host app extensions " + name); !slices.Contains(got, "src/app/Host.cs") || n != 1 {
			t.Errorf("named %s: %q (%d named), want Host.cs among them", name, got, n)
		}
	}
	ctx := context.Background()
	if files, err := r.Store.Files(ctx, 1, []string{"app/[slug]/page.tsx", "tests/*.rs", "App.java", "README.md", "Nowhere.go"}, maxFiles); err != nil ||
		!slices.Equal(files, []string{"app/[slug]/page.tsx", "tests/x/a.rs", "lib/App.java", "README.md"}) {
		t.Errorf("Files = %q %v, want a path with brackets, a glob's file, the one file of a name and the root's of another", files, err)
	}
	if files, err := r.Store.Files(ctx, 1, []string{"README.md"}, 1); err != nil || !slices.Equal(files, []string{"README.md"}) {
		t.Errorf("Files = %q %v, want the root README.md though A/ and B/ sort before it", files, err)
	}
	if files, err := r.Store.Files(ctx, 1, []string{"Extensions.cs", "src/app/Host.cs"}, maxFiles); err != nil || !slices.Equal(files, []string{"src/app/Host.cs"}) {
		t.Errorf("Files = %q %v, want Host.cs alone: six files named Extensions.cs are no one file", files, err)
	}
	// A file named past the query's first 16 words still has its first chunk read.
	words := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen "
	ids, err := r.named(ctx, 1, words+"lib/Late.go")
	if hits, _ := r.Store.Chunks(ctx, ids); err != nil || len(hits) != 1 || hits[0].Content != "func late()" {
		t.Errorf("named a file late: %+v %v, want its first chunk", hits, err)
	}
	if ids, err := r.named(ctx, 1, words+strings.Join(late, " ")); err != nil || len(ids) != namedLimit {
		t.Errorf("named eight files late: %d ids %v, want the first chunks of %d", len(ids), err, namedLimit)
	}
	// Each file's best chunk ahead of any file's second, namedPerFile a file and namedLimit in all.
	hit := func(id int64, p string) Hit { return Hit{ChunkID: id, Path: p} }
	for _, c := range []struct {
		hits []Hit
		want []int64
	}{
		{[]Hit{hit(1, "a"), hit(2, "a"), hit(3, "a"), hit(4, "a"), hit(5, "b"), hit(6, "c"), hit(7, "b"), hit(8, "d")}, []int64{1, 5, 6, 8, 2, 7}},
		{[]Hit{hit(1, "a"), hit(2, "a"), hit(3, "a"), hit(4, "a"), hit(5, "a")}, []int64{1, 2, 3}},
		{[]Hit{hit(1, "a"), hit(2, "b"), hit(3, "c"), hit(4, "d"), hit(5, "e"), hit(6, "f"), hit(7, "g")}, []int64{1, 2, 3, 4, 5, 6}},
		{nil, []int64{}},
	} {
		if got := byFile(c.hits); !slices.Equal(got, c.want) {
			t.Errorf("byFile = %v, want %v", got, c.want)
		}
	}
	if files, err := r.Store.Files(ctx, 1, []string{"src/app/Host.cs", "Host.cs", "src/"}, 6); err != nil || !slices.Equal(files, []string{"src/app/Host.cs"}) {
		t.Errorf("Files = %q %v, want Host.cs once, src/ (six more files) too wide beside it", files, err)
	}
	if files, err := r.Store.Files(ctx, 1, []string{"src/app/Host.cs", "src/"}, 7); err != nil || len(files) != 7 || files[0] != "src/app/Host.cs" {
		t.Errorf("Files = %q %v, want Host.cs, then the six others under src/", files, err)
	}
}

// TestSearchWithoutEmbeddings: a server without /embeddings leaves search to
// FTS5 and symbols, in fused order and cut to the limit.
func TestSearchWithoutEmbeddings(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'docs/notes.md', 1, 9, 'how subscribe is used', 'h1'), (1, 'src/other.ts', 1, 9, 'subscribe(a); subscribe(b)', 'h2'),
			(1, 'src/store.ts', 1, 9, 'const subscribe = (listener) => listeners.add(listener)', 'h3')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'src/store.ts', 'subscribe', 'subscribe', 'function', 1, 1)`,
	)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	r := retriever(db)
	r.Embed, r.Cache = &ai.OpenAI{BaseURL: srv.URL, ModelName: "embed"}, cache.New(1<<20)
	res, err := r.Search(context.Background(), 1, "subscribe", 2)
	if err != nil || res.Vector != 0 || res.FTS != 3 || res.Symbol != 1 || len(res.Hits) != 2 || res.Hits[0].Path != "src/store.ts" {
		t.Fatalf("got %+v %v; want the defining chunk first of two, from FTS5 and symbols", res, err)
	}
}
