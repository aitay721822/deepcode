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
	"deepcode/internal/repo/index/indexer/store"
)

func TestAutomaticSCIP(t *testing.T) {
	ctx := t.Context()
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
	run("init", "-q", "-b", "main")
	for name, content := range map[string]string{
		"go.mod":              "module example.com/main\n\ngo 1.23\n",
		"main.go":             "package main\n\nimport (\"example.com/main/alpha\"; \"example.com/main/beta\")\n\nfunc main() { alpha.Resolve(); beta.Resolve() }\nfunc again() { beta.Resolve() }\n",
		"alpha/alpha.go":      "package alpha\n\nfunc Resolve() {}\n",
		"beta/beta.go":        "package beta\n\nfunc Resolve() {}\n",
		"services/api/go.mod": "module example.com/api\n\ngo 1.23\n",
		"services/api/api.go": "package api\n\nfunc Handle() {}\nfunc Call() { Handle() }\n",
		"tools/go.mod":        "module example.com/tools\n\ngo 1.23\n",
	} {
		file := filepath.Join(remote, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "--", "go.mod", "main.go", "alpha", "beta", "services", "tools")
	run("commit", "-qm", "pinned files")
	sha := run("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(remote, "main.go"), []byte("package wrong\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := filepath.ToSlash(remote)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	loc := (&url.URL{Scheme: "file", Path: p}).String()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+loc+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/example/main.git")
	image := os.Getenv("DEEPCODE_TEST_SCIP_GO_IMAGE")
	for _, tc := range []struct {
		name, image string
		real        bool
	}{
		{"no image", "", false},
		{"missing image", "deepcode-test-missing-scip-image:never-pull", false},
		{"real scip-go", image, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.real && image == "" {
				t.Skip("set DEEPCODE_TEST_SCIP_GO_IMAGE to a built scip-go image")
			}
			data := t.TempDir()
			db, err := storage.Open(ctx, filepath.Join(data, "deepcode.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name, git_server_id) VALUES (1, 'https://github.com/example/main', 'example', 'main', 1)`); err != nil {
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
			ix := &indexer.Indexer{
				Store: &store.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}}, Events: bus,
				Git: &git.Client{Dir: data, AllowProtocol: "file"}, Servers: &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box},
				Limits: indexer.Limits{MaxFiles: 100, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20}, SCIPGoImage: tc.image,
			}
			if err := ix.Run(ctx, 1, sha, jobs.Progress{}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := ix.Store.Published(ctx, 1)
			if err != nil || snapshot == 0 {
				t.Fatalf("no published snapshot: %d, %v", snapshot, err)
			}
			assertSnapshot(t, db, snapshot, sha, tc.real)
			if tc.real {
				// The second commit imports a dependency unavailable in the
				// offline image, after a module that still indexes successfully.
				for name, content := range map[string]string{
					"services/api/go.mod": "module example.com/api\n\ngo 1.23\n\nrequire example.com/offline-dependency v1.0.0\n",
					"services/api/api.go": "package api\n\nimport _ \"example.com/offline-dependency\"\n\nfunc Handle() {}\n",
				} {
					if err := os.WriteFile(filepath.Join(remote, filepath.FromSlash(name)), []byte(content), 0644); err != nil {
						t.Fatal(err)
					}
				}
				run("add", "--", "main.go", "services/api")
				run("commit", "-qm", "add unavailable dependency")
				next := run("rev-parse", "HEAD")
				if err := ix.Run(ctx, 1, next, jobs.Progress{}); err != nil {
					t.Fatal(err)
				}
				fallback, err := ix.Store.Published(ctx, 1)
				if err != nil || fallback == snapshot {
					t.Fatalf("fallback did not publish new commit: %d, %v", fallback, err)
				}
				assertSnapshot(t, db, fallback, next, false)
				assertSnapshot(t, db, snapshot, sha, true)
			}
		})
	}
}

func assertSnapshot(t *testing.T, db *sql.DB, snapshot int64, sha string, wantSCIP bool) {
	t.Helper()
	var commit, status string
	var chunks, occurrences int
	err := db.QueryRow(`SELECT commit_sha, status, chunk_count, (SELECT count(*) FROM scip_occurrences WHERE snapshot_id = ?1)
		FROM snapshots WHERE id = ?1`, snapshot).Scan(&commit, &status, &chunks, &occurrences)
	if err != nil || commit != sha || status != "ready" || chunks == 0 || (occurrences > 0) != wantSCIP {
		t.Fatalf("snapshot %.7s %s: %d base chunks, %d SCIP occurrences (want SCIP: %t), %v", commit, status, chunks, occurrences, wantSCIP, err)
	}
	t.Logf("Published %.7s: %d base chunks, %d SCIP occurrences", commit, chunks, occurrences)
	if !wantSCIP {
		return
	}
	s := &retrieval.Store{DB: db}
	for _, tc := range []struct{ name, decl, want string }{
		{"Resolve", "alpha/alpha.go", "main.go:5"},
		{"Resolve", "beta/beta.go", "main.go:5, main.go:6"},
		{"Handle", "services/api/api.go", "services/api/api.go:4"},
	} {
		hits, found, err := s.SCIPUsers(t.Context(), snapshot, tc.name, tc.decl, 3, 4)
		var got []string
		for _, hit := range hits {
			got = append(got, fmt.Sprintf("%s:%d", hit.Path, hit.ReferenceLine))
		}
		if !found || err != nil || strings.Join(got, ", ") != tc.want {
			t.Fatalf("%s: got %v, found %t, error %v; want %s", tc.decl, got, found, err, tc.want)
		}
		t.Logf("%s:3 → %s", tc.decl, tc.want)
	}
}
