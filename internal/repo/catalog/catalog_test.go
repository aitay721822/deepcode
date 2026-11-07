package catalog

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	serverstore "deepcode/internal/repo/gitserver/store"
	"deepcode/internal/repo/index/analyzer"
)

// TestPoll: the poller queues an index job for a remote HEAD that is not the
// published commit, once: not while it is queued or running, and not again
// once it failed, even when the branch goes back to its commit later; Sync
// retries it. A HEAD whose job succeeded but is not the published commit (the
// branch went back and forth) is queued again. The job is the one of the
// configured embedding model.
func TestPoll(t *testing.T) {
	ctx := context.Background()
	remote := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", remote, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func() string {
		run("commit", "-q", "--allow-empty", "-m", "change")
		return run("rev-parse", "HEAD")
	}
	run("init", "-q", "-b", "main")
	first := commit()
	url := filepath.ToSlash(remote)
	if !strings.HasPrefix(url, "/") {
		url = "/" + url
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+url+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/demo.git")

	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/acme/demo', 'acme', 'demo')`)
	bus, err := events.New()
	if err != nil {
		t.Fatal(err)
	}
	box, err := gitserver.OpenBox(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	c := &Catalog{Store: &store.Store{DB: db}, Queue: jobs.New(&jobstore.Store{DB: db}, bus, time.Minute), Git: &git.Client{Dir: t.TempDir(), AllowProtocol: "file"},
		Servers: &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}, EmbedModel: "fake-embed"}
	poll := func() *jobs.Job {
		t.Helper()
		j, err := c.Poll(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	// publish finishes job j as an index does: its commit becomes snapshot snap, published.
	publish := func(j *jobs.Job, snap int) {
		t.Helper()
		exec(`UPDATE jobs SET status = 'succeeded' WHERE id = ?`, j.ID)
		exec(`INSERT INTO snapshots(id, repository_id, commit_sha, status, analyzer) VALUES (?, 1, ?, 'ready', ?)`, snap, j.CommitSHA, analyzer.Version)
		exec(`UPDATE repositories SET ready_snapshot_id = ?`, snap)
	}

	j1 := poll()
	if j1 == nil || j1.Kind != jobs.KindIndex || j1.CommitSHA != first {
		t.Fatalf("a repository not indexed yet polled: %+v", j1)
	}
	if again := poll(); again != nil {
		t.Fatalf("a queued commit polled again: %+v", again)
	}
	exec(`UPDATE jobs SET status = 'running' WHERE id = ?`, j1.ID)
	if again := poll(); again != nil {
		t.Fatalf("a running commit polled again: %+v", again)
	}
	publish(j1, 1)
	if again := poll(); again != nil {
		t.Fatalf("the published commit polled: %+v", again)
	}
	exec(`UPDATE snapshots SET analyzer = 0 WHERE id = 1`) // as before this version, which reads more of it
	if again := poll(); again == nil || again.ID != j1.ID || again.Status != jobs.StatusQueued {
		t.Fatalf("the published commit an older analyzer read polled: %+v", again)
	}
	exec(`UPDATE jobs SET status = 'succeeded' WHERE id = ?`, j1.ID)
	exec(`UPDATE snapshots SET analyzer = ? WHERE id = 1`, analyzer.Version)
	second := commit()
	j2 := poll()
	if j2 == nil || j2.CommitSHA != second {
		t.Fatalf("a new commit polled: %+v", j2)
	}
	exec(`UPDATE jobs SET status = 'failed' WHERE id = ?`, j2.ID)
	if again := poll(); again != nil {
		t.Fatalf("a failed commit polled again: %+v", again)
	}
	if retry, err := c.Sync(ctx, 1); err != nil || retry == nil || retry.ID != j2.ID || retry.Status != jobs.StatusQueued {
		t.Fatalf("Sync after the failure: %+v %v", retry, err)
	}
	exec(`UPDATE jobs SET status = 'succeeded' WHERE id = ?`, j2.ID)
	if again := poll(); again == nil || again.ID != j2.ID || again.Status != jobs.StatusQueued {
		t.Fatalf("a commit indexed before, but not the published one, polled: %+v", again)
	}
	exec(`UPDATE jobs SET status = 'failed' WHERE id = ?`, j2.ID)
	third := commit()
	j3 := poll()
	if j3 == nil || j3.CommitSHA != third {
		t.Fatalf("a commit after the failed one polled: %+v", j3)
	}
	publish(j3, 2)
	run("update-ref", "refs/heads/main", second) // force-pushed back to the commit whose job failed
	if again := poll(); again != nil {
		t.Fatalf("a failed commit the branch went back to polled again: %+v", again)
	}
}
