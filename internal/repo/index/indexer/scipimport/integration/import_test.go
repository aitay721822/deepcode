package scipimport_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/indexer/scipimport"
	"deepcode/internal/repo/index/indexer/store"
)

func TestManualGoImportPreservesOtherLanguages(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sha, blob := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, q := range []string{
		`INSERT INTO repositories(id,public_id,url,owner,name,git_server_id) VALUES(1,'repo','https://example.com/r','o','r',1)`,
		`INSERT INTO snapshots(id,repository_id,commit_sha,status) VALUES(1,1,'` + sha + `','ready')`,
		`INSERT INTO file_contents(hash,content) VALUES('` + blob + `','resolve
resolve
')`,
		`INSERT INTO source_files(snapshot_id,path,language,content_hash,size,line_count) VALUES(1,'source.go','go','` + blob + `',16,2),(1,'source.py','python','` + blob + `',16,2)`,
		`INSERT INTO chunks(snapshot_id,path,start_line,end_line,content,content_hash) VALUES(1,'source.go',1,2,'resolve','go'),(1,'source.py',1,2,'resolve','py')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := &store.Store{DB: db}
	old, py := "scip-go gomod p v old().", "scip-python python p v resolve()."
	rows := []store.SCIPOccurrence{{Path: "source.go", Line: 1, Symbol: old, DefinitionName: "resolve"}, {Path: "source.go", Line: 2, Symbol: old}, {Path: "source.py", Line: 1, Symbol: py, DefinitionName: "resolve"}, {Path: "source.py", Line: 2, Symbol: py}}
	if err := s.IndexSCIP(ctx, 1, rows); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		checkLanguageUsers(t, db, 1, "resolve", "source.go", 1, []string{"source.go:2"}, true)
		checkLanguageUsers(t, db, 1, "resolve", "source.py", 1, []string{"source.py:2"}, true)
	}
	check()
	newSymbol := "scip-go gomod p v new()."
	idx := &scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go"}, TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{{RelativePath: "source.go", Language: "go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 7}, Symbol: newSymbol, SymbolRoles: 1}, {Range: []int32{1, 0, 7}, Symbol: newSymbol}}}}}
	raw, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	m := map[string]any{"repository": "repo", "commit": sha, "index_sha256": hex.EncodeToString(digest[:]), "files": map[string]string{"source.go": blob}}
	manifest, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := scipimport.Import(ctx, s, nil, bytes.NewReader(manifest), bytes.NewReader(raw)); err != nil || n != 2 {
		t.Fatalf("import: %d, %v", n, err)
	}
	check()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE symbol=?`, old).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old Go rows: %d, %v", count, err)
	}
	if _, err := scipimport.Import(ctx, s, nil, bytes.NewReader(manifest), strings.NewReader("invalid index")); err == nil {
		t.Fatal("invalid import accepted")
	}
	check()
	if err := s.ReplaceSCIP(ctx, 1, []store.SCIPOccurrence{{Path: "source.go", Line: 1, Symbol: old, DefinitionName: "resolve"}, {Path: "missing.go", Line: 1, Symbol: old}}); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	check()
	if err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE symbol=?`, newSymbol).Scan(&count); err != nil || count != 2 {
		t.Fatalf("replacement rollback: %d, %v", count, err)
	}
}
