package scipimport_test

import (
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	serverstore "deepcode/internal/repo/gitserver/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graphstore"
	"deepcode/internal/repo/index/indexer"
	"deepcode/internal/repo/index/indexer/scipimport"
	"deepcode/internal/repo/index/indexer/store"
)

func reviewFixture(t *testing.T, files map[string]string, images scipimport.Images) (*sql.DB, *indexer.Indexer, string, int64) {
	t.Helper()
	remote, data := t.TempDir(), t.TempDir()
	for name, content := range files {
		p := filepath.Join(remote, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", remote, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("add", "--all")
	run("commit", "-qm", "review fixture")
	sha := run("rev-parse", "HEAD")
	p := filepath.ToSlash(remote)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+(&url.URL{Scheme: "file", Path: p}).String()+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/example/review.git")
	db, err := storage.Open(t.Context(), filepath.Join(data, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO repositories(id,url,owner,name,git_server_id) VALUES(1,'https://github.com/example/review','example','review',1)`); err != nil {
		t.Fatal(err)
	}
	bus, err := events.New()
	if err != nil {
		t.Fatal(err)
	}
	box, err := gitserver.OpenBox("", "test key")
	if err != nil {
		t.Fatal(err)
	}
	ix := &indexer.Indexer{Store: &store.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}}, Events: bus, Git: &git.Client{Dir: data, AllowProtocol: "file"}, Servers: &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}, Limits: indexer.Limits{MaxFiles: 100, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20}, SCIPImages: images}
	if err := ix.Run(t.Context(), 1, sha, jobs.Progress{}); err != nil {
		t.Fatal(err)
	}
	snap, err := ix.Store.Published(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return db, ix, sha, snap
}

func TestAutomaticSCIPReviewSiblings(t *testing.T) {
	image := os.Getenv("DEEPCODE_TEST_SCIP_TYPESCRIPT_IMAGE")
	if image == "" {
		t.Skip("TypeScript image required")
	}
	db, _, _, snap := reviewFixture(t, map[string]string{
		"app/tsconfig.json": `{"files":["caller.ts"],"references":[{"path":"../lib"}]}`,
		"app/caller.ts":     "import {resolve} from '../lib/alpha';\nresolve();\n",
		"lib/tsconfig.json": `{"compilerOptions":{"composite":true},"files":["alpha.ts"]}`,
		"lib/alpha.ts":      "export function resolve() {}\n",
	}, scipimport.Images{TypeScript: image})
	checkLanguageUsers(t, db, snap, "resolve", "lib/alpha.ts", 1, []string{"app/caller.ts:1", "app/caller.ts:2"}, true)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE snapshot_id=? AND definition_name='resolve'`, snap).Scan(&n); err != nil || n != 1 {
		t.Fatalf("deepest owner: %d %v", n, err)
	}
}

func TestAutomaticSCIPReviewStubs(t *testing.T) {
	image := os.Getenv("DEEPCODE_TEST_SCIP_PYTHON_IMAGE")
	if image == "" {
		t.Skip("Python image required")
	}
	db, _, _, snap := reviewFixture(t, map[string]string{
		"alpha.pyi": "def resolve() -> None: ...\n",
		"caller.py": "from alpha import resolve\ndef caller(): resolve()\ncaller()\n",
	}, scipimport.Images{Python: image})
	checkLanguageUsers(t, db, snap, "caller", "caller.py", 2, []string{"caller.py:3"}, true)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE snapshot_id=? AND path='alpha.pyi'`, snap).Scan(&n); err != nil || n != 0 {
		t.Fatalf("stub rows: %d %v", n, err)
	}
}

func TestAutomaticSCIPReviewEmptySolutions(t *testing.T) {
	image := os.Getenv("DEEPCODE_TEST_SCIP_TYPESCRIPT_IMAGE")
	if image == "" {
		t.Skip("TypeScript image required")
	}
	for _, tc := range []struct {
		name, config string
		covered      bool
	}{
		{"no references", `{"files":[]}`, true},
		{"empty references", `{"files":[],"references":[]}`, true},
		{"referenced project", `{"files":[],"references":[{"path":"./lib"}]}`, true},
		{"invalid option", `{"files":[],"compilerOptions":{"target":"not-a-target"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _, _, snap := reviewFixture(t, map[string]string{"tsconfig.json": tc.config, "lib/tsconfig.json": `{"files":["alpha.ts"]}`, "lib/alpha.ts": "export function resolve() {}\nresolve();\n"}, scipimport.Images{TypeScript: image})
			checkLanguageUsers(t, db, snap, "resolve", "lib/alpha.ts", 1, []string{"lib/alpha.ts:2"}, tc.covered)
		})
	}
}

func TestAutomaticSCIPReviewTopLevel(t *testing.T) {
	image := os.Getenv("DEEPCODE_TEST_SCIP_CSHARP_IMAGE")
	if image == "" {
		t.Skip("Csharp image required")
	}
	for _, tc := range []struct {
		name, caller string
		covered      bool
	}{
		{"top level", "Alpha.Resolve();\n", true},
		{"library", "class Caller { void Run() { Alpha.Resolve(); } }\n", true},
		{"compile error", "Missing.External.Resolve();\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _, _, snap := reviewFixture(t, map[string]string{"Alpha.cs": "static class Alpha { public static void Resolve() {} }\n", "Caller.cs": tc.caller, "Directory.Build.props": `<Project><Import Project="/missing/repo.props" /></Project>`, "global.json": `{"sdk":{"version":"0.0.1"}}`}, scipimport.Images{CSharp: image})
			checkLanguageUsers(t, db, snap, "Resolve", "Alpha.cs", 1, []string{"Caller.cs:1"}, tc.covered)
		})
	}
}
