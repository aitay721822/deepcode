package scipimport_test

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	retrieval "deepcode/internal/knowledge/retrieval/store"
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

func TestAutomaticSCIPLanguages(t *testing.T) {
	images := []string{os.Getenv("DEEPCODE_TEST_SCIP_GO_IMAGE"), os.Getenv("DEEPCODE_TEST_SCIP_TYPESCRIPT_IMAGE"), os.Getenv("DEEPCODE_TEST_SCIP_PYTHON_IMAGE"), os.Getenv("DEEPCODE_TEST_SCIP_RUST_IMAGE"), os.Getenv("DEEPCODE_TEST_SCIP_JAVA_IMAGE")}
	for _, image := range images {
		if image == "" {
			t.Skip("set all DEEPCODE_TEST_SCIP_{GO,TYPESCRIPT,PYTHON,RUST,JAVA}_IMAGE variables")
		}
	}
	remote := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", remote, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(remote, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	configs := map[string]string{
		"tsconfig.json":                   `{"files":[],"references":[{"path":"./typescript"},{"path":"./javascript"}]}`,
		"typescript/tsconfig.json":        `{"compilerOptions":{"jsx":"preserve","target":"ES2020"},"include":["**/*"]}`,
		"typescript/nested/tsconfig.json": `{"compilerOptions":{"target":"ES2020"},"include":["*.ts"]}`,
		"javascript/jsconfig.json":        `{"compilerOptions":{"target":"ES2020"},"include":["*.js","*.jsx","*.mjs","*.cjs"]}`,
	}
	files := map[string]string{
		"go.mod":         "module example.com/main\n\ngo 1.23\n",
		"alpha/alpha.go": "package alpha\n\nfunc Resolve() {}\n", "beta/beta.go": "package beta\n\nfunc Resolve() {}\n",
		"main.go":                 "package main\nimport (\"example.com/main/alpha\"; \"example.com/main/beta\")\nfunc main() { alpha.Resolve(); beta.Resolve() }\nfunc again() { _ = \"𝒳\"; beta.Resolve() }\n",
		"typescript/package.json": `{"name":"probe-ts","version":"1.0.0"}`,
		"typescript/alpha.ts":     "export function resolve() { return 1; }\n", "typescript/beta.ts": "export function resolve() { return 2; }\n",
		"typescript/caller.ts":      "import * as alpha from './alpha';\nimport * as beta from './beta';\nalpha.resolve();\nbeta.resolve();\nconst astral = '𝒳'; beta.resolve();\n",
		"typescript/view.tsx":       "import * as beta from './beta';\nexport const view = <>{beta.resolve()}</>;\n",
		"typescript/nested/deep.ts": "export function deep() {}\ndeep();\n",
		"typescript/module.mts":     "export function moduleResolve() {}\n", "typescript/common.cts": "export function commonResolve() {}\n",
		"javascript/package.json": `{"name":"probe-js","version":"1.0.0"}`,
		"javascript/alpha.js":     "export function resolve() { return 1; }\n", "javascript/beta.js": "export function resolve() { return 2; }\n",
		"javascript/caller.js":  "import * as alpha from './alpha';\nimport * as beta from './beta';\nalpha.resolve();\nbeta.resolve();\nconst astral = '𝒳'; beta.resolve();\n",
		"javascript/view.jsx":   "import * as beta from './beta';\nexport const view = <>{beta.resolve()}</>;\n",
		"javascript/module.mjs": "export function moduleResolve() {}\n", "javascript/common.cjs": "exports.commonResolve = function() {};\n",
		"python/alpha.py": "def resolve():\n    return 1\n", "python/beta.py": "def resolve():\n    return 2\n",
		"python/caller.py":  "from . import alpha\nfrom . import beta\nalpha.resolve()\nbeta.resolve()\nastral = '𝒳'; beta.resolve()\n",
		"rust/Cargo.toml":   "[package]\nname = \"probe\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"rust/Cargo.lock":   "version = 4\n\n[[package]]\nname = \"probe\"\nversion = \"0.1.0\"\n",
		"rust/build.rs":     "fn main() { std::fs::write(\"/tmp/deepcode-build-ran\", b\"ran\").unwrap(); panic!(\"build.rs must not run\"); }\n",
		"rust/src/alpha.rs": "pub fn resolve() {}\n", "rust/src/beta.rs": "pub fn resolve() {}\n",
		"rust/src/main.rs": "mod alpha;\nmod beta;\nfn main() {\n    alpha::resolve();\n    beta::resolve();\n    let _astral = \"𝒳\"; beta::resolve();\n}\n",
		"java/Alpha.java":  "class Alpha { static void resolve() {} }\n", "java/Beta.java": "class Beta { static void resolve() {} }\n",
		"java/caller.java": "class Caller {\n  void run() {\n    Alpha.resolve();\n    Beta.resolve();\n    String astral = \"𝒳\"; Beta.resolve();\n  }\n}\n",
	}
	run("init", "-q", "-b", "main")
	for name, content := range files {
		write(name, content)
	}
	for name, content := range configs {
		write(name, content)
	}
	run("add", "--all")
	run("commit", "-qm", "pinned multilingual sources")
	sha := run("rev-parse", "HEAD")
	write("java/caller.java", "import missing.external.Dependency;\n"+files["java/caller.java"])
	run("add", "--", "java/caller.java")
	run("commit", "-qm", "Java external dependency rejected")
	brokenJava := run("rev-parse", "HEAD")
	write("java/caller.java", files["java/caller.java"])
	write("rust/Cargo.toml", files["rust/Cargo.toml"]+"\n[dependencies]\nserde = \"1\"\n")
	run("add", "--", "rust/Cargo.toml", "java/caller.java")
	run("commit", "-qm", "registry dependency rejected offline")
	registry := run("rev-parse", "HEAD")
	write("rust/Cargo.toml", files["rust/Cargo.toml"])
	run("rm", "--", "rust/Cargo.lock")
	run("add", "--", "rust/Cargo.toml")
	run("commit", "-qm", "missing lockfile rejected")
	missingLock := run("rev-parse", "HEAD")
	write("rust/Cargo.lock", files["rust/Cargo.lock"])
	for name := range configs {
		run("rm", "--", name)
	}
	run("add", "--", "rust/Cargo.lock")
	run("commit", "-qm", "infer root project")
	inferred := run("rev-parse", "HEAD")
	for name, content := range configs {
		write(name, content)
	}
	write("typescript/zbroken/tsconfig.json", "{ invalid json")
	run("add", "--all")
	run("commit", "-qm", "broken TS project")
	broken := run("rev-parse", "HEAD")
	write("typescript/caller.ts", "uncommitted invalid contents")
	p := filepath.ToSlash(remote)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	loc := (&url.URL{Scheme: "file", Path: p}).String()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+loc+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/example/main.git")
	for _, tc := range []struct {
		name, sha                                  string
		brokenImage                                int
		wantGo, wantTS, wantPy, wantRust, wantJava bool
	}{
		{"all", sha, -1, true, true, true, true, true}, {"missing Go image", sha, 0, false, true, true, true, true},
		{"missing TS image", sha, 1, true, false, true, true, true}, {"missing Python image", sha, 2, true, true, false, true, true},
		{"missing Rust image", sha, 3, true, true, true, false, true}, {"registry Rust dependency", registry, -1, true, true, true, false, true},
		{"missing Rust lockfile", missingLock, -1, true, true, true, false, true},
		{"inferred root", inferred, -1, true, true, true, true, true}, {"broken TS project", broken, -1, true, false, true, true, true},
		{"broken Java compile", brokenJava, -1, true, true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := t.TempDir()
			db, err := storage.Open(t.Context(), filepath.Join(data, "deepcode.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`INSERT INTO repositories(id,url,owner,name,git_server_id) VALUES (1,'https://github.com/example/main','example','main',1)`); err != nil {
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
			chosen := slices.Clone(images)
			if tc.brokenImage >= 0 {
				chosen[tc.brokenImage] = "deepcode-test-missing-scip-image:never-pull"
			}
			ix := &indexer.Indexer{Store: &store.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}}, Events: bus, Git: &git.Client{Dir: data, AllowProtocol: "file"}, Servers: &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}, Limits: indexer.Limits{MaxFiles: 100, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20}, SCIPImages: scipimport.Images{Go: chosen[0], TypeScript: chosen[1], Python: chosen[2], Rust: chosen[3], Java: chosen[4]}}
			if err := ix.Run(t.Context(), 1, tc.sha, jobs.Progress{}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := ix.Store.Published(t.Context(), 1)
			if err != nil || snapshot == 0 {
				t.Fatalf("snapshot %d: %v", snapshot, err)
			}
			for _, lang := range []struct {
				name, dir, ext, ident string
				line, post, ref       int
				want                  bool
			}{
				{"Go", "", "go", "Resolve", 3, 4, 3, tc.wantGo}, {"TypeScript", "typescript/", "ts", "resolve", 1, 5, 3, tc.wantTS},
				{"JavaScript", "javascript/", "js", "resolve", 1, 5, 3, tc.wantTS}, {"Python", "python/", "py", "resolve", 1, 5, 3, tc.wantPy},
				{"Rust", "rust/src/", "rs", "resolve", 1, 6, 4, tc.wantRust},
				{"Java", "java/", "java", "resolve", 1, 5, 3, tc.wantJava},
			} {
				t.Run(lang.name, func(t *testing.T) {
					caller := lang.dir + "caller." + lang.ext
					if lang.name == "Go" {
						caller = "main.go"
					}
					if lang.name == "Rust" {
						caller = "rust/src/main.rs"
					}
					for _, which := range []string{"alpha", "beta"} {
						decl := lang.dir + which + "." + lang.ext
						if lang.name == "Java" {
							decl = lang.dir + strings.ToUpper(which[:1]) + which[1:] + ".java"
						}
						if lang.name == "Go" {
							decl = which + "/" + decl
						}
						want := []string{fmt.Sprintf("%s:%d", caller, lang.ref)}
						if which == "beta" {
							want = append(want, fmt.Sprintf("%s:%d", caller, lang.post))
							if lang.name != "Go" {
								want[0] = fmt.Sprintf("%s:%d", caller, lang.ref+1)
							}
							if lang.name == "TypeScript" {
								want = append(want, "typescript/view.tsx:2")
							}
							if lang.name == "JavaScript" {
								want = append(want, "javascript/view.jsx:2")
							}
						}
						checkLanguageUsers(t, db, snapshot, lang.ident, decl, lang.line, want, lang.want)
					}
					if lang.want {
						line := strings.Split(files[caller], "\n")[lang.post-1]
						col := strings.LastIndex(line, lang.ident)
						var count int
						err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE snapshot_id=? AND path=? AND line=? AND byte_column=? AND definition_name=''`, snapshot, caller, lang.post, col).Scan(&count)
						if err != nil || count != 1 {
							t.Fatalf("post-astral %s:%d byte %d: %d occurrences, %v", caller, lang.post, col, count, err)
						}
						t.Logf("post-astral %s:%d byte column %d", caller, lang.post, col)
					}
				})
			}
			checkLanguageUsers(t, db, snapshot, "Alpha", "java/Alpha.java", 1, []string{"java/caller.java:3"}, tc.wantJava)
			checkLanguageUsers(t, db, snapshot, "Beta", "java/Beta.java", 1, []string{"java/caller.java:4", "java/caller.java:5"}, tc.wantJava)
			t.Run("TSX", func(t *testing.T) {
				checkLanguageUsers(t, db, snapshot, "resolve", "typescript/beta.ts", 1, []string{"typescript/caller.ts:4", "typescript/caller.ts:5", "typescript/view.tsx:2"}, tc.wantTS)
			})
			if tc.wantTS {
				var count int
				err := db.QueryRow(`SELECT count(*) FROM scip_occurrences WHERE snapshot_id=? AND path='typescript/nested/deep.ts' AND definition_name='deep'`, snapshot).Scan(&count)
				if err != nil || count != 1 {
					t.Fatalf("deepest project definition count %d: %v", count, err)
				}
			}
		})
	}
}

func checkLanguageUsers(t *testing.T, db *sql.DB, snapshot int64, name, decl string, line int, want []string, enabled bool) {
	t.Helper()
	hits, found, err := (&retrieval.Store{DB: db}).SCIPUsers(t.Context(), snapshot, name, decl, line, 20)
	var got []string
	for _, hit := range hits {
		got = append(got, fmt.Sprintf("%s:%d", hit.Path, hit.ReferenceLine))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !enabled {
		want = nil
	}
	if err != nil || found != enabled || !slices.Equal(got, want) {
		t.Fatalf("%s:%d users %v found=%v error=%v; want %v found=%v", decl, line, got, found, err, want, enabled)
	}
	t.Logf("%s:%d → %v (SCIP=%v)", decl, line, got, found)
}
