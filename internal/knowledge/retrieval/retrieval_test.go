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
	graphstore "deepcode/internal/repo/index/graphstore"
)

// TestSearchKeepsNamed: the search keeps a named file's best chunk, though
// fusion ranks first what two searches find.
func TestSearchKeepsNamed(t *testing.T) {
	db := seeded(t, `INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'src/a.go', 1, 9, 'func alpha() { alpha() }', 'a'),
		(1, 'src/b.go', 1, 9, 'func beta() { beta() }', 'b'), (1, 'src/c.go', 1, 9, 'func gamma() { gamma() }', 'c'), (1, 'docs/Pinned.cs', 1, 9, 'unrelated text', 'p')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'src/a.go', 'alpha', 'alpha', 'function', 1, 1),
		(1, 'src/b.go', 'beta', 'beta', 'function', 1, 1), (1, 'src/c.go', 'gamma', 'gamma', 'function', 1, 1)`)
	if res, err := retriever(db).Search(context.Background(), 1, "alpha beta gamma docs/Pinned.cs", 1); err != nil || len(res.Hits) != 1 || res.Hits[0].Path != "docs/Pinned.cs" {
		t.Fatalf("got %+v %v, want the named file's chunk", res.Hits, err)
	}
}

// TestSearchPage: anchors' chunks come first, in order, each once, a path with
// a space read whole and a line no word finds found; the page's files rank by
// its words; an anchor past the limit, or whose line no chunk holds, is left out.
func TestSearchPage(t *testing.T) {
	ins := `INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'a/b c.cs', 1, 9, 'class Order', 'o'),
		(1, 'README.md', 1, 20, 'intro', 'r1'), (1, 'README.md', 21, 40, 'recipe', 'r2'), (1, 'README.md', 45, 60, 'more', 'r3'), (1, 'own.go', 1, 9, 'alpha', 'w'), (1, 'far.go', 1, 9, 'alpha alpha beta', 'f')`
	var many []Anchor
	for i := range 13 {
		ins, many = ins+fmt.Sprintf(", (1, 'n%02d.go', 1, 9, 'x', 'n%d')", i, i), append(many, Anchor{Path: fmt.Sprintf("n%02d.go", i)})
	}
	r := retriever(seeded(t, ins))
	search := func(files []string, anchors []Anchor, limit int) (out []string) {
		res, err := r.SearchPage(context.Background(), 1, "alpha beta", files, anchors, limit)
		for _, h := range res.Hits {
			out = append(out, fmt.Sprintf("%s:%d", h.Path, h.StartLine))
		}
		return append(out, fmt.Sprint(err))
	}
	got, top := search([]string{"own.go"}, []Anchor{{"README.md", 25}, {"a/b c.cs", 0}, {"README.md", 30}, {"README.md", 42}, {"gone.go", 1}}, 4), search(nil, many, 12)
	if !slices.Equal(got, []string{"README.md:21", "a/b c.cs:1", "own.go:1", "far.go:1", "<nil>"}) || len(top) != 13 || top[11] != "n11.go:1" || top[12] != "<nil>" {
		t.Errorf("got %q; 13 anchors at 12: %q", got, top)
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
// start with the query come before shorter ones that merely contain it; a
// guide's section is no symbol.
func TestSymbolSearch(t *testing.T) {
	db := seeded(t,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'a.go', 'xa_', 'xa_', 'function', 1, 1), (1, 'a.go', 'a_bc', 'a_bc', 'function', 2, 2),
			(1, 'a.go', 'a_', 'a_', 'function', 3, 3), (1, 'a.go', 'axb', 'axb', 'function', 4, 4),
			(1, 'README.md', 'a_ guide', 'a_ guide', 'section', 1, 9)`,
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
// case, the longer name first when nothing else tells them apart
// (store.TestDefiningOrder); only the first 32 names are looked up.
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

// TestCoded: a name counts as code by its shape or what stands beside it.
func TestCoded(t *testing.T) {
	for q, want := range map[string]string{
		"How does fromJson deserialize a string?":     "fromJson",
		"Router::nest, with_state, $store and OAuth2": "Router nest with_state $store OAuth2",
		"Gson.fromJson or Gson#toJson":                "Gson fromJson toJson",
		"the `serve` call and listen() in HTTPServer": "serve listen HTTPServer",
		"JSON over HTTP ends here. Next the router.":  "",
		"中文.Router beside (parenthesised) words":      "",
	} {
		if got := strings.Join(coded(q, bounded(identRe.FindAllString(q, -1), 32, nil)), " "); got != want {
			t.Errorf("coded(%q) = %q, want %q", q, got, want)
		}
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
// glob (names.FileNames) is ranked among the hits, though its words lose to
// every other file's; a name for more files than maxFiles names none.
func TestSearchNamedFiles(t *testing.T) {
	inserts := []string{`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
		(1, 'src/app/Host.cs', 1, 9, 'var b = Builder.Create()', 'h0'), (1, 'app/[slug]/page.tsx', 1, 9, 'page', 'hp'),
		(1, 'lib/App.java', 1, 9, 'class App', 'ha'), (1, 'tests/x/a.rs', 1, 9, 'fn t()', 'ht'),
		(1, 'README.md', 1, 9, 'intro', 'hr'), (1, 'A/README.md', 1, 9, 'a', 'hra'), (1, 'B/README.md', 1, 9, 'b', 'hrb'),
		(1, 'lib/Late.go', 20, 29, 'func later()', 'hl2'), (1, 'lib/Late.go', 10, 19, 'func late()', 'hl1'), (1, 'svc/a/Events/E.cs', 1, 9, 'class E', 'ev1'), (1, 'svc/b/Events/F.cs', 1, 9, 'class F', 'ev2')`}
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
	filesOf := func(names []string, limit int) ([]string, error) {
		groups, err := r.Store.Files(ctx, 1, names, limit)
		if len(groups) != len(names) {
			t.Errorf("Files(%q) = %d groups, want one a name", names, len(groups))
		}
		return slices.Concat(groups...), err
	}
	if files, err := filesOf([]string{"app/[slug]/page.tsx", "tests/*.rs", "App.java", "README.md", "Nowhere.go"}, maxFiles); err != nil ||
		!slices.Equal(files, []string{"app/[slug]/page.tsx", "tests/x/a.rs", "lib/App.java", "README.md"}) {
		t.Errorf("Files = %q %v, want a path with brackets, a glob's file, the one file of a name and the root's of another", files, err)
	}
	if files, err := filesOf([]string{"README.md"}, 1); err != nil || !slices.Equal(files, []string{"README.md"}) {
		t.Errorf("Files = %q %v, want the root README.md though A/ and B/ sort before it", files, err)
	}
	if files, err := filesOf([]string{"Extensions.cs", "app/Host.cs", "x/", "s0/Extensions.cs", "Events/"}, maxFiles); err != nil ||
		!slices.Equal(files, []string{"src/app/Host.cs", "tests/x/a.rs", "src/s0/Extensions.cs"}) {
		t.Errorf("Files = %q %v, want what the one file or folder each path ends names: six Extensions.cs and two projects' Events/ none", files, err)
	}
	// A file named past the query's first 16 words still has its first chunk read.
	words := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen "
	ids, _, err := r.named(ctx, 1, words+"lib/Late.go")
	if hits, _ := r.Store.Chunks(ctx, ids); err != nil || len(hits) != 1 || hits[0].Content != "func late()" {
		t.Errorf("named a file late: %+v %v, want its first chunk", hits, err)
	}
	if ids, _, err := r.named(ctx, 1, words+strings.Join(late, " ")); err != nil || len(ids) != namedLimit {
		t.Errorf("named eight files late: %d ids %v, want the first chunks of %d", len(ids), err, namedLimit)
	}
	// Six files named first take the places, though the seventh's words match best.
	ids, firsts, err := r.named(ctx, 1, "host app extensions "+strings.Join(late[:6], " ")+" src/s0/Extensions.cs")
	hits, _ := r.Store.Chunks(ctx, ids)
	var paths []string
	for _, h := range hits {
		paths = append(paths, h.Path)
	}
	if err != nil || !slices.Equal(paths, late[:6]) || firsts != 6 {
		t.Errorf("named seven files: %q %v, want the first six named", paths, err)
	}
	if ids, firsts, err := r.named(ctx, 1, "late later lib/Late.go far/L0.go"); err != nil || len(ids) != 3 || firsts != 2 {
		t.Errorf("named two files, one found twice: %d ids, %d first %v; want 3 and 2", len(ids), firsts, err)
	}
	// Each file's best chunk ahead of any file's second, in the order of the
	// files, namedPerFile a file and namedLimit in all.
	hit := func(id int64, p string) Hit { return Hit{ChunkID: id, Path: p} }
	for _, c := range []struct {
		files []string
		hits  []Hit
		want  []int64
	}{
		{[]string{"a", "b", "c", "d"}, []Hit{hit(1, "a"), hit(2, "a"), hit(3, "a"), hit(4, "a"), hit(5, "b"), hit(6, "c"), hit(7, "b"), hit(8, "d")}, []int64{1, 5, 6, 8, 2, 7}},
		{[]string{"d", "c", "b", "a"}, []Hit{hit(1, "a"), hit(2, "a"), hit(5, "b"), hit(6, "c"), hit(8, "d")}, []int64{8, 6, 5, 1, 2}},
		{[]string{"a"}, []Hit{hit(1, "a"), hit(2, "a"), hit(3, "a"), hit(4, "a"), hit(5, "a")}, []int64{1, 2, 3}},
		{[]string{"a", "b", "c", "d", "e", "f", "g"}, []Hit{hit(1, "a"), hit(2, "b"), hit(3, "c"), hit(4, "d"), hit(5, "e"), hit(6, "f"), hit(7, "g")}, []int64{1, 2, 3, 4, 5, 6}},
		{nil, nil, []int64{}},
	} {
		if got, _ := byFile(c.files, c.hits); !slices.Equal(got, c.want) {
			t.Errorf("byFile(%q) = %v, want %v", c.files, got, c.want)
		}
	}
	// Each name's next file in turn, a name's files by their best hit, those
	// no word finds last; namedLimit files at most.
	for _, c := range []struct {
		groups [][]string
		hits   []Hit
		want   []string
	}{
		{[][]string{{"x/a", "x/b", "x/c"}, {"g"}, nil, {"h"}}, []Hit{hit(1, "x/c"), hit(2, "h"), hit(3, "x/a")}, []string{"x/c", "g", "h", "x/a", "x/b"}},
		{[][]string{{"x/1", "x/2", "x/3", "x/4", "x/5", "x/6", "x/7", "x/8"}, {"g"}, {"h"}}, []Hit{hit(1, "x/8")}, []string{"x/8", "g", "h", "x/1", "x/2", "x/3"}},
		{[][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}, {"g"}}, []Hit{hit(1, "g"), hit(2, "f")}, []string{"a", "b", "c", "d", "e", "f"}},
		{nil, nil, nil},
	} {
		if got := pick(c.groups, c.hits); !slices.Equal(got, c.want) {
			t.Errorf("pick(%q) = %q, want %q", c.groups, got, c.want)
		}
	}
	if files, err := filesOf([]string{"src/app/Host.cs", "Host.cs", "src/"}, 6); err != nil || !slices.Equal(files, []string{"src/app/Host.cs"}) {
		t.Errorf("Files = %q %v, want Host.cs once, src/ (six more files) too wide beside it", files, err)
	}
	if files, err := filesOf([]string{"src/app/Host.cs", "src/"}, 7); err != nil || len(files) != 7 || files[0] != "src/app/Host.cs" {
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

// TestWeigh: a test's or a generated file's cosine is weighed down, code's not.
func TestWeigh(t *testing.T) {
	if weigh("src/a_test.go", false) != secondaryCosine || weigh("src/a.go", true) != secondaryCosine ||
		weigh("src/a.go", false) != 1 || secondaryCosine >= 1 {
		t.Fatal("weigh: tests and generated files want secondaryCosine (< 1), code 1")
	}
}
