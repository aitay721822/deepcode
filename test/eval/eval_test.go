package eval

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	serverstore "deepcode/internal/repo/gitserver/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graph/store"
	"deepcode/internal/repo/index/indexer"
	indexstore "deepcode/internal/repo/index/indexer/store"
	"deepcode/internal/repo/index/vectorstore"
)

func TestRetrieval(t *testing.T) {
	embed, err := Embedder()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range Corpora {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			db, snap := index(t, source(t, c), c.SHA, embed)
			ret := &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}},
				Embed: embed, Vectors: &vectorstore.SQLite{DB: db}, Cache: cache.New(1 << 20)}
			res, err := Run(context.Background(), ret, snap, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range res.Misses {
				t.Log(m)
			}
			t.Logf("%s: recall@%d %.3f, MRR %.3f over %d questions", c.Name, Hits, res.Recall, res.MRR, len(c.Questions))
			recall, mrr := c.Recall, c.MRR
			if embed != nil {
				if embed.ModelName != HybridModel {
					t.Skipf("no floors for the embedding model %s, only for %s", embed.ModelName, HybridModel)
				}
				recall, mrr = c.HybridRecall, c.HybridMRR
			}
			if res.Recall < recall || res.MRR < mrr {
				t.Errorf("below the floors, recall %.3f and MRR %.3f", recall, mrr)
			}
		})
	}
}

// source is the git directory to fetch c's commit from: this repository's
// own for deepcode, else c.Name+".git" under DEEPCODE_EVAL_GIT. The corpus is
// skipped when there is none, or it lacks the commit (a shallow checkout).
func source(t *testing.T, c Corpus) string {
	var dir string
	if c.URL == "" {
		out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
		if err != nil {
			t.Skipf("not in a git checkout: %v", err)
		}
		dir = strings.TrimSpace(string(out))
	} else if root := os.Getenv("DEEPCODE_EVAL_GIT"); root != "" {
		dir = filepath.Join(root, c.Name+".git")
	} else {
		t.Skipf("set DEEPCODE_EVAL_GIT to a folder holding %s.git (git clone --bare %s %s.git)", c.Name, c.URL, c.Name)
	}
	if !strings.HasSuffix(dir, ".git") || exec.Command("git", "--git-dir="+dir, "cat-file", "-e", c.SHA+"^{commit}").Run() != nil {
		t.Skipf("%s is not a .git directory holding commit %.7s", dir, c.SHA)
	}
	return dir
}

// index indexes the repository in the git directory dir at sha, as an index
// job does with cmd/deepcode's default limits and embed (nil: no vectors),
// into a database of its own, and returns that and the snapshot.
func index(t *testing.T, dir, sha string, embed *ai.OpenAI) (*sql.DB, int64) {
	ctx, tmp := context.Background(), t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(tmp, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The git server fetches url+".git".
	url := "file:///" + strings.TrimPrefix(filepath.ToSlash(strings.TrimSuffix(dir, ".git")), "/")
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, ?, 'eval', 'eval')`, url); err != nil {
		t.Fatal(err)
	}
	bus, err := events.New()
	if err != nil {
		t.Fatal(err)
	}
	box, err := gitserver.OpenBox(tmp, "eval")
	if err != nil {
		t.Fatal(err)
	}
	ix := &indexer.Indexer{Store: &indexstore.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}},
		Git: &git.Client{Dir: tmp, AllowProtocol: "file"}, Servers: &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}, Events: bus,
		Embed: embed, Vectors: &vectorstore.SQLite{DB: db},
		Limits: indexer.Limits{MaxFiles: 20000, MaxFileBytes: 512 << 10, MaxTotalBytes: 200 << 20}}
	if err := ix.Run(ctx, 1, sha, jobs.Progress{}); err != nil {
		t.Fatal(err)
	}
	var snap int64
	if err := db.QueryRow(`SELECT ready_snapshot_id FROM repositories WHERE id = 1`).Scan(&snap); err != nil {
		t.Fatal(err)
	}
	return db, snap
}

func TestScore(t *testing.T) {
	hits := func(paths ...string) []retrieval.Hit {
		out := make([]retrieval.Hit, len(paths))
		for i, p := range paths {
			out[i] = retrieval.Hit{Path: p}
		}
		return out
	}
	if recall, rr := score(hits("a", "b", "a", "c"), []string{"c", "x"}); recall != 0.5 || rr != 0.25 {
		t.Errorf("recall %v, reciprocal rank %v; want 0.5 and 0.25", recall, rr)
	}
	if recall, rr := score(hits("a"), []string{"x"}); recall != 0 || rr != 0 {
		t.Errorf("nothing found: %v %v", recall, rr)
	}
	// file#Name wants the chunk holding the symbol, not just the file.
	chunks := []retrieval.Hit{{Path: "c.go", Symbol: "Context, Context.Reset"}, {Path: "c.go", Symbol: "Context.RoutePattern, RouteParams"}}
	if recall, rr := score(chunks, []string{"c.go#RoutePattern"}); recall != 1 || rr != 0.5 {
		t.Errorf("by symbol: %v %v", recall, rr)
	}
	if recall, _ := score(chunks[:1], []string{"c.go#RoutePattern"}); recall != 0 {
		t.Errorf("the file's other chunk counted: %v", recall)
	}
}
