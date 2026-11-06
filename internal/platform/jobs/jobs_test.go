package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deepcode/internal/platform/events"
	"deepcode/internal/platform/storage"
)

func setup(t *testing.T) (*Queue, *sql.DB) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []int{1, 2} {
		if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (?, ?, 'o', 'r')`, id, "https://github.com/o/r"+string(rune('0'+id))); err != nil {
			t.Fatal(err)
		}
	}
	bus, err := events.New()
	if err != nil {
		t.Fatal(err)
	}
	return New(db, bus, time.Minute), db
}

func TestEnqueueIsIdempotent(t *testing.T) {
	q, db := setup(t)
	ctx := context.Background()
	a, _ := q.EnqueueIndex(ctx, 1, "sha", "m")
	b, _ := q.EnqueueIndex(ctx, 1, "sha", "m")
	c, _ := q.EnqueueIndex(ctx, 1, "sha", "other-model")
	if a.ID != b.ID || a.ID == c.ID {
		t.Fatalf("ids %d %d %d", a.ID, b.ID, c.ID)
	}
	for _, finished := range []string{"failed", "succeeded"} { // e.g. a force-push back to sha
		db.Exec(`UPDATE jobs SET status = ?, attempts = 3, error = 'x' WHERE id = ?`, finished, a.ID)
		again, _ := q.EnqueueIndex(ctx, 1, "sha", "m")
		if again.ID != a.ID || again.Status != "queued" || again.Attempts != 0 || again.Error != "" {
			t.Fatalf("%s job not re-queued: %+v", finished, again)
		}
	}
	db.Exec(`UPDATE jobs SET status = 'running' WHERE id = ?`, a.ID)
	if j, _ := q.EnqueueIndex(ctx, 1, "sha", "m"); j.Status != "running" {
		t.Fatalf("running job was reset: %+v", j)
	}
}

func TestClaimSerializesPerRepository(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()
	q.EnqueueIndex(ctx, 1, "a", "")
	q.EnqueueWiki(ctx, 1, "a")
	q.EnqueueIndex(ctx, 2, "b", "")
	first, _ := q.claim(ctx)
	second, _ := q.claim(ctx)
	if first.RepoID != 1 || second.RepoID != 2 {
		t.Fatalf("claimed repos %d then %d, want 1 then 2", first.RepoID, second.RepoID)
	}
	if _, ok := q.claim(ctx); ok {
		t.Fatal("claimed a second job for a repository that is already running one")
	}
}

func TestRunRecordsOutcomes(t *testing.T) {
	q, db := setup(t)
	ctx := context.Background()
	runOne := func(h Handler) Job {
		t.Helper()
		j, ok := q.claim(ctx)
		if !ok {
			t.Fatal("nothing to claim")
		}
		q.run(ctx, j, h)
		got, _ := q.Get(ctx, j.ID)
		return got
	}

	q.EnqueueIndex(ctx, 1, "ok", "")
	if j := runOne(func(context.Context, Job, Progress) error { return nil }); j.Status != "succeeded" {
		t.Fatalf("success: %+v", j)
	}

	// A transient failure runs again 30s later, then 60s; the last attempt fails the job.
	q.EnqueueIndex(ctx, 1, "flaky", "")
	flaky := func(_ context.Context, _ Job, p Progress) error { p.Stage("embed", 3, 9); return errors.New("timeout") }
	wait := func(id int64) (s int) { // seconds until the job may run again
		db.QueryRow(`SELECT unixepoch(run_after) - unixepoch('now') FROM jobs WHERE id = ?`, id).Scan(&s)
		return s
	}
	j := runOne(flaky)
	if w := wait(j.ID); j.Status != "queued" || j.Attempts != 1 || j.Error != "timeout" || j.Stage != "embed" || j.Progress != 3 || w < 20 || w > 30 {
		t.Fatalf("transient failure: %+v (runs again in %ds)", j, w)
	}
	db.Exec(`UPDATE jobs SET run_after = CURRENT_TIMESTAMP WHERE id = ?`, j.ID)
	j = runOne(flaky)
	if w := wait(j.ID); j.Status != "queued" || j.Attempts != 2 || w < 50 || w > 60 {
		t.Fatalf("second failure: %+v (runs again in %ds)", j, w)
	}
	db.Exec(`UPDATE jobs SET run_after = CURRENT_TIMESTAMP WHERE id = ?`, j.ID)
	if j = runOne(flaky); j.Status != "failed" || j.Attempts != maxAttempts || j.Error != "timeout" {
		t.Fatalf("last attempt: %+v", j)
	}

	q.EnqueueIndex(ctx, 1, "too-big", "")
	if j := runOne(func(context.Context, Job, Progress) error { return Permanent(errors.New("too many files")) }); j.Status != "failed" {
		t.Fatalf("permanent failure: %+v", j)
	}

	q.EnqueueIndex(ctx, 1, "panics", "")
	if j := runOne(func(context.Context, Job, Progress) error { panic("bad file") }); j.Error != "panic: bad file" {
		t.Fatalf("panic: %+v", j)
	}
}

// TestPrune: jobs that finished over 30 days ago go, with their output; recent
// and unfinished jobs stay, and so does each repository's newest job.
func TestPrune(t *testing.T) {
	q, db := setup(t)
	ctx := context.Background()
	var ids []int64
	for _, sha := range []string{"old", "running", "recent", "newest"} {
		j, _ := q.EnqueueIndex(ctx, 1, sha, "")
		ids = append(ids, j.ID)
	}
	only, _ := q.EnqueueIndex(ctx, 2, "only", "")
	Progress{q, ids[0]}.Logf("output")
	db.Exec(`UPDATE jobs SET status = 'succeeded', updated_at = datetime('now', '-31 days') WHERE id IN (?, ?)`, ids[0], only.ID)
	db.Exec(`UPDATE jobs SET status = 'running', updated_at = datetime('now', '-31 days') WHERE id = ?`, ids[1])
	db.Exec(`UPDATE jobs SET status = 'succeeded' WHERE id = ?`, ids[2])
	db.Exec(`UPDATE jobs SET status = 'failed', updated_at = datetime('now', '-40 days') WHERE id = ?`, ids[3])
	q.prune(ctx)
	var left []string
	rows, _ := db.Query(`SELECT commit_sha FROM jobs ORDER BY id`)
	for rows.Next() {
		var sha string
		rows.Scan(&sha)
		left = append(left, sha)
	}
	rows.Close()
	var output int
	db.QueryRow(`SELECT count(*) FROM job_logs`).Scan(&output)
	if strings.Join(left, ",") != "running,recent,newest,only" || output != 0 {
		t.Fatalf("left %v and %d lines of output", left, output)
	}
}

// TestLatestOf: each repository's newest job, a queued or running one first.
func TestLatestOf(t *testing.T) {
	q, db := setup(t)
	ctx := context.Background()
	older, _ := q.EnqueueIndex(ctx, 1, "older", "")
	q.EnqueueIndex(ctx, 1, "newer", "")
	db.Exec(`UPDATE jobs SET status = 'succeeded'`)
	q.EnqueueIndex(ctx, 1, "older", "") // re-queued: the one to watch
	latest, err := q.LatestOf(ctx, []int64{1, 2})
	if err != nil || len(latest) != 1 || latest[1].ID != older.ID {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	if none, err := q.LatestOf(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("no repositories: %+v %v", none, err)
	}
}

func TestShutdownHandsJobBack(t *testing.T) {
	q, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	q.EnqueueIndex(ctx, 1, "long", "")
	j, _ := q.claim(ctx)
	q.run(ctx, j, func(ctx context.Context, _ Job, _ Progress) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	got, _ := q.Get(context.Background(), j.ID)
	if got.Status != "queued" || got.Attempts != 0 {
		t.Fatalf("interrupted job: %+v", got)
	}
}

// TestWorkersRunRecoveredJobs runs the event-driven path: a job left running
// by a dead process is recovered, the sweeper announces it on the bus and a
// subscribed worker claims and runs it.
func TestWorkersRunRecoveredJobs(t *testing.T) {
	q, db := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	j, _ := q.EnqueueIndex(ctx, 1, "orphan", "") // announced before anyone subscribed: lost
	if _, err := db.Exec(`UPDATE jobs SET status = 'running', attempts = 1 WHERE id = ?`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	q.Register(ctx, 2, map[string]Handler{KindIndex: func(context.Context, Job, Progress) error { close(done); return nil }})
	stopped := make(chan struct{})
	go func() {
		q.Bus.Run(ctx)
		close(stopped)
	}()
	go q.Sweep(ctx)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("recovered job was not picked up")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("bus did not stop after cancel")
	}
	if got, _ := q.Get(context.Background(), j.ID); got.Status != "succeeded" {
		t.Fatalf("job after run: %+v", got)
	}
}

// TestWorkersAreBounded: Watermill handles every announcement in its own
// goroutine, so the worker slots alone must cap how many jobs run at once.
func TestWorkersAreBounded(t *testing.T) {
	q, db := setup(t)
	for id := 3; id <= 6; id++ {
		if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (?, ?, 'o', 'r')`, id, fmt.Sprintf("https://github.com/o/r%d", id)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var running, peak atomic.Int32
	q.Register(ctx, 2, map[string]Handler{KindIndex: func(context.Context, Job, Progress) error {
		n := running.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(100 * time.Millisecond)
		running.Add(-1)
		return nil
	}})
	go q.Bus.Run(ctx)
	<-q.Bus.Running()
	for repo := int64(1); repo <= 6; repo++ { // one job per repository: none waits for another
		if _, err := q.EnqueueIndex(ctx, repo, "sha", ""); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for n := 0; n < 6; db.QueryRow(`SELECT count(*) FROM jobs WHERE status = 'succeeded'`).Scan(&n) {
		if time.Now().After(deadline) {
			t.Fatalf("%d of 6 jobs finished", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p := peak.Load(); p != 2 {
		t.Fatalf("%d jobs ran at once with 2 workers", p)
	}
}

// TestRecoverFailsLastAttempt: a job that was running when the process died
// is queued again, unless that was its last attempt.
func TestRecoverFailsLastAttempt(t *testing.T) {
	q, db := setup(t)
	ctx := context.Background()
	a, _ := q.EnqueueIndex(ctx, 1, "a", "")
	b, _ := q.EnqueueIndex(ctx, 2, "b", "")
	db.Exec(`UPDATE jobs SET status = 'running', attempts = 1 WHERE id = ?`, a.ID)
	db.Exec(`UPDATE jobs SET status = 'running', attempts = 3 WHERE id = ?`, b.ID)
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	ga, _ := q.Get(ctx, a.ID)
	gb, _ := q.Get(ctx, b.ID)
	if ga.Status != "queued" || gb.Status != "failed" || gb.Error == "" {
		t.Fatalf("recovered: %+v and %+v", ga, gb)
	}
}

// TestOutput: streamed text lands in the job's log line by line, in order;
// the admin listing names the repository; re-queuing clears the old output.
func TestOutput(t *testing.T) {
	q, db := setup(t)
	ctx := context.Background()
	j, _ := q.EnqueueIndex(ctx, 1, "sha", "")
	write, flush := Progress{q, j.ID}.Lines()
	write("first line\nsec")
	write("ond\nthird")
	flush()
	lines, _ := q.Logs(ctx, j.ID, 0, 10)
	if len(lines) != 3 || lines[0].Line != "first line" || lines[1].Line != "second" || lines[2].Line != "third" {
		t.Fatalf("lines: %+v", lines)
	}
	if after, _ := q.Logs(ctx, j.ID, lines[0].ID, 10); len(after) != 2 {
		t.Fatalf("lines after the first: %+v", after)
	}
	all := func(int64) bool { return true }
	listed, _ := q.List(ctx, "queued", 10, all)
	if len(listed) != 1 || listed[0].ID != j.ID || listed[0].RepoOwner != "o" || listed[0].RepoName != "r" {
		t.Fatalf("listed: %+v", listed)
	}
	if none, _ := q.List(ctx, "failed", 10, all); len(none) != 0 {
		t.Fatalf("status filter: %+v", none)
	}
	// Hidden repositories are skipped before the limit, not after it.
	q.EnqueueIndex(ctx, 2, "sha2", "")
	if mine, _ := q.List(ctx, "", 1, func(repo int64) bool { return repo == 1 }); len(mine) != 1 || mine[0].ID != j.ID {
		t.Fatalf("visible filter: %+v", mine)
	}
	db.Exec(`UPDATE jobs SET status = 'succeeded' WHERE id = ?`, j.ID)
	q.EnqueueIndex(ctx, 1, "sha", "")
	if left, _ := q.Logs(ctx, j.ID, 0, 10); len(left) != 0 {
		t.Fatalf("re-queued job kept %d old lines", len(left))
	}
}

// TestOutputCapped: a sink logs maxLines lines, then one saying the rest is
// left out, and nothing more.
func TestOutputCapped(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()
	j, _ := q.EnqueueIndex(ctx, 1, "sha", "")
	write, flush := Progress{q, j.ID}.Lines()
	write(strings.Repeat("line\n", maxLines))
	write("one more")
	write(" and more\nand more\n")
	flush()
	lines, _ := q.Logs(ctx, j.ID, 0, maxLines+10)
	if n := len(lines); n != maxLines+1 || lines[n-2].Line != "line" || !strings.Contains(lines[n-1].Line, "left out") {
		t.Fatalf("%d lines, ending %+v", n, lines[max(0, n-2):])
	}
}

// TestFollow: Follow passes every line after the cursor once, in order, and
// ends once the finished job's last line is out; a finished job's output is
// passed at once.
func TestFollow(t *testing.T) {
	q, db := setup(t)
	ctx := context.Background()
	j, _ := q.EnqueueIndex(ctx, 1, "sha", "")
	p := Progress{q, j.ID}
	p.Logf("one")
	p.Logf("two")
	first, _ := q.Logs(ctx, j.ID, 0, 1)
	var lines []string
	done := make(chan Listed, 1)
	go func() {
		end, err := q.Follow(ctx, j.ID, first[0].ID, func(l LogLine) { lines = append(lines, l.Line) }, func(Listed) {})
		if err != nil {
			t.Error(err)
		}
		done <- end
	}()
	p.Stage("parse", 1, 2)
	p.Logf("three")
	db.Exec(`UPDATE jobs SET status = 'succeeded' WHERE id = ?`, j.ID)
	q.changed(events.TopicJobUpdated, j.ID)
	select {
	case end := <-done:
		if end.Status != StatusSucceeded || strings.Join(lines, ",") != "two,three" {
			t.Fatalf("followed %q until %+v", lines, end)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Follow went on after the job finished")
	}
	var all []string
	var seen []string
	end, err := q.Follow(ctx, j.ID, 0, func(l LogLine) { all = append(all, l.Line) }, func(l Listed) { seen = append(seen, l.Status) })
	if err != nil || end.Status != StatusSucceeded || strings.Join(all, ",") != "one,two,three" || len(seen) != 1 {
		t.Fatalf("finished job: %q %q %+v %v", all, seen, end, err)
	}
}
