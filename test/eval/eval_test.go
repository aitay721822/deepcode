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
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/index/indexer"
)

func TestRetrieval(t *testing.T) {
	for _, c := range Corpora {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			db, snap := index(t, source(t, c), c.SHA)
			res, err := Run(context.Background(), &retrieval.Retriever{DB: db}, snap, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range res.Misses {
				t.Log(m)
			}
			t.Logf("%s: recall@%d %.3f, MRR %.3f over %d questions", c.Name, Hits, res.Recall, res.MRR, len(c.Questions))
			if res.Recall < c.Recall || res.MRR < c.MRR {
				t.Errorf("below the floors, recall %.3f and MRR %.3f", c.Recall, c.MRR)
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
// job does with cmd/deepcode's default limits, into a database of its own,
// and returns that and the snapshot.
func index(t *testing.T, dir, sha string) (*sql.DB, int64) {
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
	ix := &indexer.Indexer{DB: db, Git: &git.Client{Dir: tmp, AllowProtocol: "file"},
		Servers: &gitserver.Store{DB: db, Box: box}, Events: bus,
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
	if recall, rr := score([]string{"a", "b", "a", "c"}, []string{"c", "x"}); recall != 0.5 || rr != 0.25 {
		t.Errorf("recall %v, reciprocal rank %v; want 0.5 and 0.25", recall, rr)
	}
	if recall, rr := score([]string{"a"}, []string{"x"}); recall != 0 || rr != 0 {
		t.Errorf("nothing found: %v %v", recall, rr)
	}
}
