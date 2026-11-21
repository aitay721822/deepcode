package scipimport_test

import (
	"database/sql"
	"fmt"
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

func reviewGenerate(t *testing.T, ix *indexer.Indexer, sha string, snap int64, images scipimport.Images) string {
	t.Helper()
	remote, err := ix.Servers.Remote(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ix.Git.Fetch(t.Context(), 1, remote, sha)
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	if _, err := scipimport.Generate(t.Context(), ix.Store, ix.Git, dir, sha, snap, images, func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }); err != nil {
		t.Fatal(err)
	}
	return strings.Join(logs, "\n")
}

func TestAutomaticSCIPReviewSiblings(t *testing.T) {
	image := os.Getenv("DEEPCODE_TEST_SCIP_TYPESCRIPT_IMAGE")
	if image == "" {
		t.Skip("TypeScript image required")
	}
	db, _, _, snap := reviewFixture(t, map[string]string{
		"app/package.json":  `{"name":"app"}`,
		"lib/package.json":  `{"name":"lib"}`,
		"app/tsconfig.json": `{"files":["caller.ts"],"references":[{"path":"../lib"}]}`,
		"app/caller.ts":     "import {resolve} from '../lib/alpha';\nresolve();\nexport function caller() {}\ncaller();\n",
		"lib/tsconfig.json": `{"compilerOptions":{"composite":true},"files":["alpha.ts"]}`,
		"lib/alpha.ts":      "export function resolve() {}\nresolve();\n",
	}, scipimport.Images{TypeScript: image})
	checkLanguageUsers(t, db, snap, "resolve", "lib/alpha.ts", 1, []string{"app/caller.ts:1", "app/caller.ts:2", "lib/alpha.ts:2"}, true)
	checkLanguageUsers(t, db, snap, "caller", "app/caller.ts", 3, []string{"app/caller.ts:4"}, true)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE snapshot_id=? AND definition_name='resolve'`, snap).Scan(&n); err != nil || n != 1 {
		t.Fatalf("deepest owner: %d %v", n, err)
	}
}

func TestAutomaticSCIPReviewSiblingCollision(t *testing.T) {
	images := scipimport.Images{TypeScript: os.Getenv("DEEPCODE_TEST_SCIP_TYPESCRIPT_IMAGE"), Python: os.Getenv("DEEPCODE_TEST_SCIP_PYTHON_IMAGE")}
	if images.TypeScript == "" || images.Python == "" {
		t.Skip("TypeScript and Python images required")
	}
	db, ix, sha, snap := reviewFixture(t, map[string]string{
		"app/tsconfig.json": `{"files":["alpha.ts","caller.ts"]}`,
		"app/alpha.ts":      "export function resolve() {}\n",
		"app/caller.ts":     "import {resolve} from './alpha';\nresolve();\n",
		"lib/tsconfig.json": `{"compilerOptions":{"composite":true},"files":["alpha.ts","caller.ts"]}`,
		"lib/alpha.ts":      "export function resolve() {}\n",
		"lib/caller.ts":     "import {resolve} from './alpha';\nresolve();\n",
		"source.py":         "def resolve(): pass\nresolve()\n",
	}, scipimport.Images{})
	log := reviewGenerate(t, ix, sha, snap, images)
	if !strings.Contains(log, `SCIP projects "app" and "lib" define the same symbol`) {
		t.Fatalf("missing collision rejection: %s", log)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE snapshot_id=? AND path LIKE '%.ts'`, snap).Scan(&n); err != nil || n != 0 {
		t.Fatalf("TypeScript rows: %d %v", n, err)
	}
	checkLanguageUsers(t, db, snap, "resolve", "source.py", 1, []string{"source.py:2"}, true)
	t.Log(log)
}

func TestAutomaticSCIPReviewSiblingOwnership(t *testing.T) {
	image := os.Getenv("DEEPCODE_TEST_SCIP_TYPESCRIPT_IMAGE")
	if image == "" {
		t.Skip("TypeScript image required")
	}
	for _, mode := range []string{"packageless", "named", "ancestor named", "single packageless"} {
		t.Run(mode, func(t *testing.T) {
			files := map[string]string{
				"safe/tsconfig.json": `{"files":["safe.ts"]}`,
				"safe/safe.ts":       "export function safe() {}\nsafe();\n",
			}
			if mode != "single packageless" {
				for name, content := range map[string]string{
					"app/nested/tsconfig.json": `{"include":["*.ts","../../lib/src/alpha.ts"]}`,
					"app/nested/alpha.ts":      "export function resolve() {}\n",
					"app/nested/caller.ts":     "import {resolve as own} from './alpha';\nimport {resolve} from '../../lib/src/alpha';\nown();\nresolve();\n",
					"lib/tsconfig.json":        `{"files":["src/alpha.ts","src/caller.ts"]}`,
					"lib/src/alpha.ts":         "export function resolve() {}\n",
					"lib/src/caller.ts":        "import {resolve} from './alpha';\nresolve();\n",
				} {
					files[name] = content
				}
			}
			if mode == "named" {
				files["app/nested/package.json"] = `{"name":"app"}`
				files["lib/package.json"] = `{"name":"lib"}`
			} else if mode == "ancestor named" {
				files["package.json"] = `{"name":"ancestor"}`
			}
			db, ix, sha, snap := reviewFixture(t, files, scipimport.Images{})
			log := reviewGenerate(t, ix, sha, snap, scipimport.Images{TypeScript: image})
			covered := mode == "named" || mode == "single packageless"
			checkLanguageUsers(t, db, snap, "safe", "safe/safe.ts", 1, []string{"safe/safe.ts:2"}, covered)
			if !covered {
				if !strings.Contains(log, `SCIP project "app/nested": packageless TypeScript/JavaScript project emitted document outside its root`) {
					t.Fatalf("missing project rejection: %s", log)
				}
				var n int
				if err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE snapshot_id=?`, snap).Scan(&n); err != nil || n != 0 {
					t.Fatalf("unsafe rows published: %d %v", n, err)
				}
			} else if mode == "named" {
				checkLanguageUsers(t, db, snap, "resolve", "app/nested/alpha.ts", 1, []string{"app/nested/caller.ts:1", "app/nested/caller.ts:3"}, true)
				checkLanguageUsers(t, db, snap, "resolve", "lib/src/alpha.ts", 1, []string{"app/nested/caller.ts:2", "app/nested/caller.ts:4", "lib/src/caller.ts:1", "lib/src/caller.ts:2"}, true)
			}
			t.Log(log)
		})
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

func TestAutomaticSCIPReviewKeywordMembers(t *testing.T) {
	image := os.Getenv("DEEPCODE_TEST_SCIP_TYPESCRIPT_IMAGE")
	if image == "" {
		t.Skip("TypeScript image required")
	}
	for _, ext := range []string{"ts", "js"} {
		t.Run(ext, func(t *testing.T) {
			name := "source." + ext
			db, _, _, snap := reviewFixture(t, map[string]string{"tsconfig.json": `{"compilerOptions":{"allowJs":true},"include":["*"]}`, name: "class Store { delete() {} }\nnew Store().delete();\n"}, scipimport.Images{TypeScript: image})
			checkLanguageUsers(t, db, snap, "delete", name, 1, []string{name + ":2"}, true)
		})
	}
}
