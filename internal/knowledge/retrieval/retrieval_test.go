package retrieval

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/platform/storage"
)

func TestFuse(t *testing.T) {
	got := fuse([]int64{1, 2, 3}, []int64{3, 4}, []int64{3, 2})
	if want := []int64{3, 2, 1, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFTSQuery(t *testing.T) {
	if got, want := ftsQuery("How does the getUserName function work in auth_service?"), `"getusername"* OR "auth_service"*`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if got := ftsQuery("講解vscode的幾個組件"); got != `"vscode"*` {
		t.Fatalf("got %s", got)
	}
}

// TestReadme: the first two chunks of the top-level README, in order; a
// file in a folder named readme is not the repository's README.
func TestReadme(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'README.md', 40, 60, 'later', 'h1'), (1, 'README.md', 1, 20, 'intro', 'h2'), (1, 'README.md', 21, 39, 'usage', 'h3'),
			(1, 'readme/guide.md', 1, 9, 'nested', 'h4'), (1, 'main.go', 1, 9, 'code', 'h5')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
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
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
			VALUES (1, 'tests/store.test.ts', 1, 9, 'subscribe(a); subscribe(b); setState(1); expect(a).toHaveBeenCalled()', 'h1')`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
			VALUES (1, 'src/store.ts', 1, 9, 'const setState = (next) => { state = next; listeners.forEach((l) => l(state)) }
			const subscribe = (listener) => { listeners.add(listener) }', 'h2')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := (&Retriever{DB: db}).fts(context.Background(), 1, "How does setState notify subscribers?")
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
	if ids, _ := (&Retriever{DB: db}).fts(context.Background(), 1, "subscribers"); len(ids) != 2 {
		t.Fatalf("\"subscribers\" matched %d chunks, want both (stemmed to subscrib)", len(ids))
	}
}

// TestSymbolSearch: % and _ in a query match only themselves, and names that
// start with the query come before shorter ones that merely contain it.
func TestSymbolSearch(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES
			(1, 'a.go', 'xa_', 'xa_', 'function', 1, 1), (1, 'a.go', 'a_bc', 'a_bc', 'function', 2, 2),
			(1, 'a.go', 'a_', 'a_', 'function', 3, 3), (1, 'a.go', 'axb', 'axb', 'function', 4, 4)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := (&Retriever{DB: db}).Symbols(context.Background(), 1, "a_", 10)
	var names []string
	for _, h := range hits {
		names = append(names, h.Name)
	}
	if err != nil || strings.Join(names, " ") != "a_ a_bc xa_" {
		t.Fatalf("%v %v", names, err)
	}
}

// TestSymbolNamesBounded: the first 32 names in a query are looked up, and
// the 33rd is not.
func TestSymbolNamesBounded(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'sha')`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'main.go', 1, 9, 'func Target() {}', 'h1')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'main.go', 'Target', 'Target', 'function', 1, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
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
}
