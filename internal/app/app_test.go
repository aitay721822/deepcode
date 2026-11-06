package app

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
)

// TestPollRound: a git server that stays unreachable is logged once, not
// every round, and again if it fails after answering in between; a round
// that panics is logged instead of stopping the server.
func TestPollRound(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/acme/demo', 'acme', 'demo')`); err != nil {
		t.Fatal(err)
	}
	bus, err := events.New()
	if err != nil {
		t.Fatal(err)
	}
	box, err := gitserver.OpenBox(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	// Only file: remotes are allowed, so every ls-remote of the https remote fails the same way.
	cat := &catalog.Catalog{DB: db, Queue: jobs.New(db, bus, time.Minute), Git: &git.Client{Dir: t.TempDir(), AllowProtocol: "file"},
		Servers: &gitserver.Store{DB: db, Box: box}}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	failing := map[int64]string{}
	round := func() { pipeline{catalog: cat}.pollRound(ctx, failing) }
	failures := func() int { return strings.Count(logs.String(), "poll acme/demo: git ") }
	round()
	round()
	// Once, by the poller, with git's own words: not by Sync's error mapping every round.
	if n := failures(); n != 1 || strings.Contains(logs.String(), "catalog: ") {
		t.Fatalf("an unreachable remote logged %d times in two rounds, want once:\n%s", n, &logs)
	}
	// A local repository answers for the remote once, then the remote fails as before.
	remote := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "c"}} {
		if out, err := exec.Command("git", append([]string{"-C", remote}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	url := filepath.ToSlash(remote)
	if !strings.HasPrefix(url, "/") {
		url = "/" + url
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+url+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/demo.git")
	round()
	t.Setenv("GIT_CONFIG_COUNT", "0")
	round()
	if n := failures(); n != 2 || !strings.Contains(logs.String(), "poll acme/demo: indexing the new commit") {
		t.Fatalf("a failure after an answer: %d failures logged in all, want 2:\n%s", n, &logs)
	}
	pipeline{}.pollRound(ctx, failing) // no catalog: the round panics
	if !strings.Contains(logs.String(), "poll: panic:") {
		t.Fatalf("the panic was not logged:\n%s", &logs)
	}
}

// TestBadSettingCreatesNoAdmin: a setting that stops the start is caught
// before the first administrator is created, so no one-time password is
// printed for a server that never ran.
func TestBadSettingCreatesNoAdmin(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := New(ctx, Config{DataDir: dir, TrustedProxies: []string{"proxy.local"}}, db, http.NotFoundHandler(), true); err == nil {
		t.Fatal("started with a proxy that is no address")
	}
	var users int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("%d users after the failed start (%v)", users, err)
	}
}
