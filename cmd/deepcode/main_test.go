package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	retrieval "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/platform/storage"
	index "deepcode/internal/repo/index/indexer/store"
)

// TestFrozenCommands: the commands, arguments and flags v1.0.0 has are still
// there (README's compatibility promise): a release may add commands and
// flags, never drop or rename one of these.
func TestFrozenCommands(t *testing.T) {
	cmds := map[string]*cobra.Command{}
	for _, c := range []*cobra.Command{serveCmd(), backupCmd(), passwdCmd()} {
		cmds[c.Name()] = c
	}
	args := map[string][]string{"serve": nil, "backup": {"backup.db"}, "passwd": {"admin"}}
	for name, flags := range map[string][]string{"serve": {"addr", "data"}, "backup": {"data"}, "passwd": {"data"}} {
		c := cmds[name]
		if c == nil {
			t.Errorf("v1.0.0's command %s is gone", name)
			continue
		}
		if err := c.ValidateArgs(args[name]); err != nil {
			t.Errorf("v1.0.0's %s %v: %v", name, args[name], err)
		}
		for _, f := range flags {
			if c.Flags().Lookup(f) == nil {
				t.Errorf("v1.0.0's flag %s --%s is gone", name, f)
			}
		}
	}
}

const probeModule = "example.com/scip-probe"

type scipFixture struct {
	db                     *sql.DB
	worktree, data, commit string
	repo                   string
	moduleRoot             string
	files                  map[string]string
}

func TestSCIPGoPilot(t *testing.T) {
	scipGo, err := exec.LookPath("scip-go")
	if err != nil {
		t.Skip("install scip-go v0.2.10 to run the real artifact test")
	}
	for _, moduleRoot := range []string{"", "services/demo"} {
		t.Run(path.Join("repo", moduleRoot), func(t *testing.T) {
			checkRealSCIP(t, scipGo, moduleRoot)
		})
	}
}

func checkRealSCIP(t *testing.T, scipGo, moduleRoot string) {
	t.Helper()
	f := newSCIPFixture(t, moduleRoot)
	artifact := filepath.Join(f.data, "index.scip")
	cmd := exec.CommandContext(t.Context(), scipGo, "index",
		"--repository-remote="+probeModule, "--module-path="+probeModule,
		"--module-version=0.0.1", "--skip-tests", "--output", artifact, "./...")
	cmd.Dir = filepath.Join(f.worktree, f.moduleRoot)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scip-go: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	f.importArtifact(t, raw, f.commit, f.files, "")
	f.checkReferences(t)
	var skipped int
	if err := f.db.QueryRow("SELECT count(*) FROM scip_occurrences WHERE path = ?", path.Join(moduleRoot, "skipped.go")).Scan(&skipped); err != nil || skipped != 0 {
		t.Fatalf("the skipped Go file has %d occurrences, err=%v", skipped, err)
	}
}

func TestSCIPImport(t *testing.T) {
	f := newSCIPFixture(t, "")
	db := &retrieval.Store{DB: f.db}
	if hits, found, err := db.SCIPUsers(t.Context(), 1, "Resolve", "alpha/alpha.go", 3, 4); found || len(hits) != 0 || err != nil {
		t.Fatalf("before import: %v, found=%t, err=%v", hits, found, err)
	}
	const alpha = "scip-go gomod example.com/scip-probe 0.0.1 `example.com/scip-probe/alpha`/Resolve()."
	const beta = "scip-go gomod example.com/scip-probe 0.0.1 `example.com/scip-probe/beta`/Resolve()."
	const localPrint = "scip-go gomod example.com/scip-probe 0.0.1 `example.com/scip-probe/alpha`/Print()."
	const stdPrint = "scip-go gomod github.com/golang/go/src go1.25 log/Print()."
	doc := func(path string, occurrences ...*scip.Occurrence) *scip.Document {
		return &scip.Document{RelativePath: path, Language: "go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
			Occurrences: occurrences}
	}
	occ := func(symbol string, line, col int32, definition bool) *scip.Occurrence {
		o := &scip.Occurrence{Symbol: symbol}
		if definition {
			o.SymbolRoles = int32(scip.SymbolRole_Definition)
		}
		length := int32(len("Resolve"))
		if strings.Contains(symbol, "Print") {
			length = int32(len("Print"))
		}
		r, err := scip.NewRange([]int32{line, col, col + length})
		if err != nil {
			t.Fatal(err)
		}
		o.SetSourceRange(r)
		return o
	}
	idx := &scip.Index{
		Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go"}, TextDocumentEncoding: scip.TextEncoding_UTF8},
		Documents: []*scip.Document{
			doc("alpha/alpha.go", occ(alpha, 2, 5, true), occ(localPrint, 3, 5, true)),
			doc("beta/beta.go", occ(beta, 2, 5, true)),
			doc("main.go", occ(alpha, 8, 18, false), occ(beta, 8, 34, false), occ(beta, 12, 6, false), occ(stdPrint, 13, 5, false)),
			doc("skipped.go", occ(alpha, 2, 9, false)),
		},
	}
	marshal := func() []byte {
		t.Helper()
		raw, err := proto.Marshal(idx)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := marshal()
	f.importArtifact(t, raw, f.commit, f.files, "")
	f.checkReferences(t)

	f.importArtifact(t, raw, strings.Repeat("a", 40), f.files, "does not name a ready snapshot")
	f.importArtifact(t, raw, f.commit, map[string]string{
		"alpha/alpha.go": strings.Repeat("b", 40), "beta/beta.go": f.files["beta/beta.go"], "main.go": f.files["main.go"], "skipped.go": f.files["skipped.go"],
	}, "does not match a Go blob")
	skippedHash := f.files["skipped.go"]
	f.files["skipped.go"] = strings.Repeat("b", 40)
	f.importArtifact(t, raw, f.commit, f.files, "does not match a Go blob")
	f.files["skipped.go"] = skippedHash
	maliciousRoot := f
	maliciousRoot.moduleRoot = "../external"
	maliciousRoot.importArtifact(t, raw, f.commit, f.files, "module_root must be a repository-relative directory")
	idx.Documents[2].Occurrences[0] = occ(alpha, 8, 5, false) // inside the emoji
	f.importArtifact(t, marshal(), f.commit, f.files, "invalid SCIP source range")
	idx.Documents[2].RelativePath = "../main.go"
	f.importArtifact(t, marshal(), f.commit, f.files, "invalid, duplicate, or unlisted SCIP document")
	f.checkReferences(t) // failed imports leave the previous index intact

	store := &index.Store{DB: f.db}
	if err := store.MarkPruned(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := store.DropRows(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRow("SELECT count(*) FROM scip_occurrences").Scan(&count); err != nil || count != 0 {
		t.Fatalf("pruned SCIP rows: %d, err=%v", count, err)
	}
}

func (f scipFixture) checkReferences(t *testing.T) {
	t.Helper()
	db := &retrieval.Store{DB: f.db}
	mainPath := path.Join(f.moduleRoot, "main.go")
	for _, tc := range []struct {
		name, path, want string
	}{
		{"Resolve", "alpha/alpha.go", "main.go:9"},
		{"Resolve", "beta/beta.go", "main.go:9, main.go:13"},
		{"Print", "alpha/alpha.go", ""},
	} {
		line := 3
		if tc.name == "Print" {
			line = 4
		}
		hits, found, err := db.SCIPUsers(t.Context(), 1, tc.name, path.Join(f.moduleRoot, tc.path), line, 4)
		var got []string
		for _, hit := range hits {
			if hit.Path != mainPath {
				t.Errorf("reference path %q, want %q", hit.Path, mainPath)
			}
			got = append(got, fmt.Sprintf("%s:%d", hit.Path, hit.ReferenceLine))
		}
		t.Logf("%s %s:%d → %v", tc.name, tc.path, line, got)
		if want := strings.ReplaceAll(tc.want, "main.go", mainPath); !found || err != nil || strings.Join(got, ", ") != want {
			t.Errorf("%s in %s: got %v, found=%t, err=%v; want %s", tc.name, tc.path, got, found, err, want)
		}
	}
	rows, err := f.db.Query(`SELECT byte_column, symbol FROM scip_occurrences
		WHERE snapshot_id = 1 AND path = ? AND line = 9 AND symbol LIKE '%/Resolve().'
		ORDER BY byte_column`, mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i, want := range []struct {
		col int
		pkg string
	}{{18, "/alpha`/"}, {34, "/beta`/"}} {
		var col int
		var symbol string
		if !rows.Next() {
			t.Fatalf("missing same-line reference %d: %v", i, rows.Err())
		}
		if err := rows.Scan(&col, &symbol); err != nil || col != want.col || !strings.Contains(symbol, want.pkg) {
			t.Fatalf("same-line reference %d: column %d, symbol %s, err=%v", i, col, symbol, err)
		}
		t.Logf("main.go:9 byte %d → %s", col, symbol)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra same-line references or query error: %v", rows.Err())
	}
	var external string
	if err := f.db.QueryRow(`SELECT symbol FROM scip_occurrences WHERE snapshot_id = 1 AND path = ? AND line = 14
		AND symbol LIKE '%Print().'`, mainPath).Scan(&external); err != nil || !strings.Contains(external, "github.com/golang/go/src") {
		t.Fatalf("log.Print: %q, %v", external, err)
	}
	t.Logf("main.go:14 → %s", external)
	if hits, found, err := db.SCIPUsers(t.Context(), 2, "Resolve", "alpha/alpha.go", 3, 4); found || len(hits) != 0 || err != nil {
		t.Fatalf("other snapshot: %v, found=%t, err=%v", hits, found, err)
	}
}

func newSCIPFixture(t *testing.T, moduleRoot string) scipFixture {
	t.Helper()
	f := scipFixture{worktree: t.TempDir(), data: t.TempDir(), moduleRoot: moduleRoot, files: map[string]string{}}
	var err error
	f.db, err = storage.Open(t.Context(), filepath.Join(f.data, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", f.worktree, "-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "--object-format=sha1")
	var files []index.File
	for name, body := range map[string]string{
		"go.mod":         "module " + probeModule + "\n\ngo 1.25\n",
		"alpha/alpha.go": "package alpha\n\nfunc Resolve() {}\nfunc Print() {}\n",
		"beta/beta.go":   "package beta\n\nfunc Resolve() {}\n",
		"skipped.go":     "package main\n\nvar _ = \"" + strings.Repeat("x", 4100) + "\"\n",
		"main.go":        "package main\n\nimport (\n\"log\"\nnamed \"" + probeModule + "/alpha\"\n\"" + probeModule + "/beta\"\n)\nfunc main() {\n\t/* 🐦 */ named.Resolve(); beta.Resolve()\n}\n\nfunc another() {\n\tbeta.Resolve()\n\tlog.Print(\"hi\")\n}\n",
	} {
		path := path.Join(f.moduleRoot, name)
		full := filepath.Join(f.worktree, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		hash := git("hash-object", "--", path)
		f.files[path] = hash
		if name == "skipped.go" {
			continue
		}
		lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
		chunks := []index.Chunk{{StartLine: 1, EndLine: len(lines), Content: body, Hash: hash}}
		if name == "main.go" {
			chunks = []index.Chunk{
				{StartLine: 8, EndLine: 10, Content: strings.Join(lines[7:10], "\n"), Hash: "main"},
				{StartLine: 12, EndLine: 15, Content: strings.Join(lines[11:15], "\n"), Hash: "another"},
			}
		}
		files = append(files, index.File{Path: path, Lang: "go", Hash: hash, Content: body, Lines: len(lines), Size: int64(len(body)), Chunks: chunks})
	}
	git("add", ".")
	f.commit = git("commit-tree", git("write-tree"), "-m", "fixture")
	git("update-ref", "HEAD", f.commit)
	if err := os.MkdirAll(filepath.Join(f.data, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("clone", "--quiet", "--bare", ".", filepath.Join(f.data, "repos", "1.git"))
	if _, err := f.db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://example.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, ?, 'ready'), (2, 1, 'other', 'ready')`, f.commit); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow("SELECT public_id FROM repositories WHERE id = 1").Scan(&f.repo); err != nil {
		t.Fatal(err)
	}
	if err := (&index.Store{DB: f.db}).Write(t.Context(), 1, 0, files, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f scipFixture) importArtifact(t *testing.T, artifact []byte, commit string, files map[string]string, wantErr string) {
	t.Helper()
	indexFile, manifest := filepath.Join(f.data, "index.scip"), filepath.Join(f.data, "manifest.json")
	digest := sha256.Sum256(artifact)
	raw, err := json.Marshal(map[string]any{"repository": f.repo, "commit": commit, "module_root": f.moduleRoot, "files": files, "index_sha256": hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{indexFile: artifact, manifest: raw} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := importSCIPCmd()
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs([]string{"--data", f.data, manifest, indexFile})
	var output bytes.Buffer
	cmd.SetOut(&output)
	err = cmd.ExecuteContext(t.Context())
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("import: got %v, want error containing %q", err, wantErr)
		}
	} else if err != nil {
		t.Fatal(err)
	} else {
		t.Log(strings.TrimSpace(output.String()))
	}
}
