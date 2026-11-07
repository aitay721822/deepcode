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

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/storage"
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

// TestReadme: the first two chunks of the top-level README, in order; a
// file in a folder named readme is not the repository's README.
func TestReadme(t *testing.T) {
	db := seeded(t,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'README.md', 40, 60, 'later', 'h1'), (1, 'README.md', 1, 20, 'intro', 'h2'), (1, 'README.md', 21, 39, 'usage', 'h3'),
			(1, 'readme/guide.md', 1, 9, 'nested', 'h4'), (1, 'main.go', 1, 9, 'code', 'h5')`,
	)
	hits, err := (&Retriever{DB: db}).Readme(context.Background(), 1)
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
	ids, err := (&Retriever{DB: db}).fts(context.Background(), 1, "How does setState notify subscribers?", nil, ftsLimit)
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
	if ids, _ := (&Retriever{DB: db}).fts(context.Background(), 1, "subscribers", nil, ftsLimit); len(ids) != 2 {
		t.Fatalf("\"subscribers\" matched %d chunks, want both (stemmed to subscrib)", len(ids))
	}
}

// TestFTSFindsWordsOfIdentifiers: a question's word finds the compound
// identifiers made of it through their terms, ahead of the same word in
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
		if ids, err := (&Retriever{DB: db}).fts(context.Background(), 1, q, nil, ftsLimit); err != nil || !reflect.DeepEqual(ids, []int64{1, 2}) {
			t.Errorf("%q found chunks %v %v, want refreshUserToken's (1), then the prose (2)", q, ids, err)
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
	hits, err := (&Retriever{DB: db}).Symbols(context.Background(), 1, "a_", 10)
	var names []string
	for _, h := range hits {
		names = append(names, h.Name)
	}
	if err != nil || strings.Join(names, " ") != "a_ a_bc xa_" {
		t.Fatalf("%v %v", names, err)
	}
	if hits, _ := (&Retriever{DB: db}).Symbols(context.Background(), 1, "a_", 2); len(hits) != 2 {
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
	r := &Retriever{DB: db}
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
	r := &Retriever{DB: db}
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
	r := &Retriever{DB: db, Embed: &ai.OpenAI{BaseURL: srv.URL, ModelName: "embed"}, Cache: cache.New(1 << 20)}
	res, err := r.Search(context.Background(), 1, "subscribe", 2)
	if err != nil || res.Vector != 0 || res.FTS != 3 || res.Symbol != 1 || len(res.Hits) != 2 || res.Hits[0].Path != "src/store.ts" {
		t.Fatalf("got %+v %v; want the defining chunk first of two, from FTS5 and symbols", res, err)
	}
}
