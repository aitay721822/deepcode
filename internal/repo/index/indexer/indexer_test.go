package indexer

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/chunker"
)

// TestIndexable: non-empty files of a known language up to the per-file size
// are indexed, unless their path hides characters or is not UTF-8; too many files, or too much
// text, fails the job for good.
func TestIndexable(t *testing.T) {
	entries := []git.TreeEntry{
		{Path: "main.go", SHA: "a", Size: 10},
		{Path: "evil\u202egnp.go", SHA: "f", Size: 10},
		{Path: "caf\xe9.go", SHA: "g", Size: 10}, // Latin-1
		{Path: "logo.png", SHA: "b", Size: 10},
		{Path: "empty.go", SHA: "c", Size: 0},
		{Path: "huge.ts", SHA: "d", Size: 101},
		{Path: "web/app.ts", SHA: "e", Size: 40},
	}
	ix := &Indexer{Limits: Limits{MaxFiles: 2, MaxFileBytes: 100, MaxTotalBytes: 50}}
	files, err := ix.indexable(entries)
	var paths []string
	for _, f := range files {
		paths = append(paths, f.path)
	}
	if err != nil || !reflect.DeepEqual(paths, []string{"main.go", "web/app.ts"}) {
		t.Fatalf("indexable: %v %v", paths, err)
	}
	permanent := reflect.TypeOf(jobs.Permanent(errors.New(""))) // retrying cannot shrink a repository
	for name, limits := range map[string]Limits{
		"files": {MaxFiles: 1, MaxFileBytes: 100, MaxTotalBytes: 50},
		"bytes": {MaxFiles: 2, MaxFileBytes: 100, MaxTotalBytes: 49},
	} {
		ix.Limits = limits
		if _, err := ix.indexable(entries); reflect.TypeOf(err) != permanent || !strings.Contains(err.Error(), "exceeds the limit") {
			t.Errorf("over the %s limit: %v", name, err)
		}
	}
}

// TestNextBatch: a batch ends at fileBatch files or before passing batchBytes,
// and always takes a file, however large.
func TestNextBatch(t *testing.T) {
	batches := func(files []file) []int {
		var got []int
		for i := 0; i < len(files); {
			b := nextBatch(files, i)
			got = append(got, len(b))
			i += len(b)
		}
		return got
	}
	var sized []file
	for _, s := range []int64{batchBytes + 1, batchBytes/2 + 1, batchBytes/2 + 1, 1} {
		sized = append(sized, file{size: s})
	}
	if got := batches(sized); !reflect.DeepEqual(got, []int{1, 1, 2}) {
		t.Errorf("by size: %v", got)
	}
	if got := batches(make([]file, fileBatch+5)); !reflect.DeepEqual(got, []int{fileBatch, 5}) {
		t.Errorf("by count: %v", got)
	}
}

// TestParseFile: text is stored as UTF-8 with LF endings; binary and minified
// files are rejected.
func TestParseFile(t *testing.T) {
	p := analyzer.NewParser()
	defer p.Close()
	lang, _ := analyzer.Detect("a.go")
	f := file{path: "a.go", lang: lang}
	longest := strings.Repeat("x", maxLineLen)
	for name, c := range map[string]struct {
		raw, content string
		ok           bool
		lines        int
	}{
		"crlf":     {"package a\r\n\r\nfunc F() {}\r\n", "package a\n\nfunc F() {}\n", true, 3},
		"invalid":  {"package a // \xff\n", "package a // �\n", true, 1},
		"longest":  {longest, longest, true, 1},
		"binary":   {"package a\x00", "", false, 0},
		"late NUL": {strings.Repeat("// text\n", 2000) + "\x00", "", false, 0}, // past the 8000 bytes git looks at
		"minified": {longest + "x", "", false, 0},
	} {
		got, ok := parseFile(p, f, []byte(c.raw))
		if ok != c.ok || got.content != c.content || got.lines != c.lines {
			t.Errorf("%s: ok %v, %d lines, content %q", name, ok, got.lines, got.content)
		}
	}
	got, _ := parseFile(p, f, []byte("package a\r\n\r\nimport \"x/y\"\r\n\r\nfunc F() {}\r\n"))
	if len(got.symbols) != 1 || got.symbols[0].Name != "F" || len(got.chunks) == 0 || !reflect.DeepEqual(got.imports, []string{"x/y"}) {
		t.Fatalf("symbols %+v, imports %q, %d chunks", got.symbols, got.imports, len(got.chunks))
	}
}

// TestParseResumes: a run stopped midway is finished by the next one, which
// parses only the files the first did not commit.
func TestParseResumes(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	for name, body := range map[string]string{"a.go": "package a\n\nfunc A() {}\n", "b.go": "package a\n\nfunc B() {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	sha := run("commit-tree", run("write-tree"), "-m", "first")
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, ?)`, sha); err != nil {
		t.Fatal(err)
	}
	ix := &Indexer{DB: db, Git: &git.Client{Dir: dir}, Limits: Limits{MaxFiles: 10, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20}}
	parse := func() error { return ix.parse(ctx, filepath.Join(dir, ".git"), sha, 1, 0, jobs.Progress{}) }
	count := func() (files, chunks int) {
		db.QueryRow(`SELECT count(*) FROM source_files WHERE snapshot_id = 1`).Scan(&files)
		db.QueryRow(`SELECT count(*) FROM chunks WHERE snapshot_id = 1`).Scan(&chunks)
		return files, chunks
	}
	if err := parse(); err != nil {
		t.Fatal(err)
	}
	files, chunks := count()
	for _, table := range []string{"chunks", "symbols", "source_files"} { // b.go's batch never committed
		db.Exec(`DELETE FROM ` + table + ` WHERE snapshot_id = 1 AND path = 'b.go'`)
	}
	if err := parse(); err != nil { // parsing a.go again would break UNIQUE (snapshot_id, path)
		t.Fatalf("resume: %v", err)
	}
	if f, c := count(); files != 2 || f != files || c != chunks {
		t.Fatalf("after the resume: %d files and %d chunks, want 2 and %d", f, c, chunks)
	}
}

// TestPruneSweepsContents: past the published snapshot and the one before
// it, snapshots lose their files, and file contents no snapshot uses go too.
func TestPruneSweepsContents(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status, indexed_at) VALUES
			(1, 1, 'a', 'ready', '2026-01-01'), (2, 1, 'b', 'ready', '2026-01-02'), (3, 1, 'c', 'ready', '2026-01-03')`,
		`INSERT INTO file_contents(hash, content) VALUES ('old', 'x'), ('kept', 'y')`,
		`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES
			(1, 'old.go', 'go', 'old', 1, 1), (1, 'kept.go', 'go', 'kept', 1, 1), (3, 'kept.go', 'go', 'kept', 1, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&Indexer{DB: db}).prune(ctx, 1, 3); err != nil {
		t.Fatal(err)
	}
	var status, hashes string
	db.QueryRow(`SELECT status FROM snapshots WHERE id = 1`).Scan(&status)
	db.QueryRow(`SELECT group_concat(hash) FROM file_contents`).Scan(&hashes)
	if status != "pruned" || hashes != "kept" {
		t.Fatalf("snapshot 1 is %s; contents left: %s", status, hashes)
	}
}

// TestReuseCoversEveryRange: every chunk whose content has a stored vector gets
// it, in each range of chunk ids up to the last, chunks that share one content
// included.
func TestReuseCoversEveryRange(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const chunks = `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?2)
		INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) SELECT ?1, 'f' || i, 1, 1, '', 'h' || min(i, ?3) FROM n`
	const want = 2*reuseBatch + 1 // the last range holds only the snapshot's last chunk
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r')`, nil},
		{`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'a'), (2, 1, 'b')`, nil},
		{chunks, []any{1, want, want}},
		{chunks, []any{2, want, want - 2}}, // its last three chunks, across the last two ranges, share one content
		{`INSERT INTO chunk_embeddings(chunk_id, model, dimension, embedding) SELECT id, 'm', 1, x'0000803f' FROM chunks WHERE snapshot_id = 1`, nil},
	} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&Indexer{DB: db}).reuse(ctx, 2, "m"); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM chunk_embeddings e JOIN chunks c ON c.id = e.chunk_id WHERE c.snapshot_id = 2`).Scan(&n)
	if n != want {
		t.Fatalf("%d of %d chunks got the stored vector", n, want)
	}
}

// TestTerms: a parsed chunk is stored with the words of its compound
// identifiers, and an unchanged file's chunks are copied with theirs;
// FillTerms gives the chunks an older version stored theirs, past one batch,
// and full-text search then finds every one by a word.
func TestTerms(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'a'), (2, 1, 'b')`); err != nil {
		t.Fatal(err)
	}
	ix := &Indexer{DB: db}
	f := parsedFile{file: file{path: "auth/userToken.go", sha: "s", lang: "go", size: 1}, content: "x", lines: 1,
		chunks: []chunker.Chunk{{StartLine: 1, EndLine: 1, Content: "func getUserToken() {}"}}}
	if err := ix.write(ctx, 1, 0, []parsedFile{f}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ix.write(ctx, 2, 1, nil, []string{f.path}); err != nil { // unchanged in snapshot 2
		t.Fatal(err)
	}
	const want = "user token get user token"
	terms := func() string {
		var s string
		db.QueryRow(`SELECT group_concat(DISTINCT coalesce(terms, 'NULL')) FROM chunks`).Scan(&s)
		return s
	}
	if got := terms(); got != want {
		t.Fatalf("stored and copied chunks hold terms %q, want %q", got, want)
	}
	// As an older version left them: those two, and a batch's worth more.
	if _, err := db.Exec(`UPDATE chunks SET terms = NULL;
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
		SELECT 2, 'auth/userToken.go', i + 1, i + 1, 'func getUserToken() {}', 'h' || i FROM n`, termsBatch); err != nil {
		t.Fatal(err)
	}
	n, err := ix.FillTerms(ctx)
	if err != nil || n != termsBatch+2 || terms() != want {
		t.Fatalf("filled %d of %d chunks (%v); terms %q", n, termsBatch+2, err, terms())
	}
	var found int
	db.QueryRow(`SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH 'terms:token'`).Scan(&found)
	if found != termsBatch+2 {
		t.Fatalf("full-text search finds %d chunks by a word of their identifiers, want %d", found, termsBatch+2)
	}
	// A chunk stored again at an id FillTerms read, with other content, keeps NULL for the next pass.
	if _, err := db.Exec(`UPDATE chunks SET terms = NULL WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if set, err := ix.setTerms(ctx, []filling{{id: 1, hash: "what FillTerms read", terms: "stale"}}); err != nil || set != 0 {
		t.Fatalf("set %d (%v), want 0", set, err)
	}
	var stale sql.NullString
	db.QueryRow(`SELECT terms FROM chunks WHERE id = 1`).Scan(&stale)
	if stale.Valid {
		t.Fatalf("terms worked out from other content were written: %q", stale.String)
	}
}

// TestImports: a parsed file's imports are stored with its content, NULL in
// a language no grammar reads, and fill in those of a content an older
// version stored; pruning a snapshot drops its edges.
func TestImports(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'a');
		INSERT INTO file_contents(hash, content) VALUES ('old', 'import "./b"')`); err != nil {
		t.Fatal(err)
	}
	ix := &Indexer{DB: db}
	files := []parsedFile{
		{file: file{path: "a.ts", sha: "old", lang: "typescript"}, content: `import "./b"`, imports: []string{"./b"}},
		{file: file{path: "b.ts", sha: "b", lang: "typescript"}, content: "export {}"},
		{file: file{path: "c.md", sha: "c", lang: "markdown"}, content: "# c"},
	}
	if err := ix.write(ctx, 1, 0, files, nil); err != nil {
		t.Fatal(err)
	}
	var got string
	db.QueryRow(`SELECT group_concat(hash || '=' || coalesce(imports, 'NULL'), ' ') FROM (SELECT * FROM file_contents ORDER BY hash)`).Scan(&got)
	if got != "b= c=NULL old=./b" {
		t.Fatalf("stored imports: %q", got)
	}
	if _, err := db.Exec(`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'a.ts', 'b.ts')`); err != nil {
		t.Fatal(err)
	}
	if err := ix.dropSnapshotData(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var edges int
	db.QueryRow(`SELECT count(*) FROM file_edges`).Scan(&edges)
	if edges != 0 {
		t.Fatalf("%d edges left after pruning", edges)
	}
}

// TestSealedTokenFailsForGood: a token the secret key cannot decrypt fails
// the job at once, since no retry can decrypt it.
func TestSealedTokenFailsForGood(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before, _ := gitserver.OpenBox("", "the key before")
	now, _ := gitserver.OpenBox("", "another key")
	// Server 1 is the GitHub server every database starts with.
	if _, err := db.Exec(`UPDATE git_servers SET token = ? WHERE id = 1`, before.Seal("ghp-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name, git_server_id) VALUES (1, 'https://github.com/o/r', 'o', 'r', 1)`); err != nil {
		t.Fatal(err)
	}
	ix := &Indexer{DB: db, Servers: &gitserver.Store{DB: db, Box: now}}
	err = ix.Run(ctx, 1, strings.Repeat("a", 40), jobs.Progress{})
	if reflect.TypeOf(err) != reflect.TypeOf(jobs.Permanent(errors.New(""))) || !errors.Is(err, gitserver.ErrSealed) {
		t.Fatalf("a sealed token: %v", err)
	}
}
