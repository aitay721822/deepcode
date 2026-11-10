package app

import (
	"bytes"
	"context"
	"database/sql"
	"io"
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
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	catalogstore "deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	serverstore "deepcode/internal/repo/gitserver/store"
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
	cat := &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: jobs.New(&jobstore.Store{DB: db}, bus, time.Minute),
		Git: &git.Client{Dir: t.TempDir(), AllowProtocol: "file"}, Servers: &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}}
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
	// A local repository answers for the remote twice (its return logged once), then the remote fails as before.
	localRemote(t)
	round()
	round()
	t.Setenv("GIT_CONFIG_COUNT", "0")
	round()
	if n := failures(); n != 2 || !strings.Contains(logs.String(), "poll acme/demo: indexing the new commit") ||
		strings.Count(logs.String(), "poll acme/demo: working again") != 1 {
		t.Fatalf("a failure after an answer: %d failures logged in all, want 2:\n%s", n, &logs)
	}
	pipeline{}.pollRound(ctx, failing) // no catalog: the round panics
	if !strings.Contains(logs.String(), "poll: panic:") {
		t.Fatalf("the panic was not logged:\n%s", &logs)
	}
}

// TestRunPolls: with SyncEvery set, Run checks the remotes by itself and
// queues the index job of the commit a remote moved to.
func TestRunPolls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/acme/demo', 'acme', 'demo')`); err != nil {
		t.Fatal(err)
	}
	localRemote(t)
	log.SetOutput(io.Discard) // the new administrator's one-time password
	defer log.SetOutput(os.Stderr)
	a, err := New(ctx, Config{DataDir: dir, SyncEvery: 20 * time.Millisecond, GitProtocols: "file"}, db, http.NotFoundHandler(), true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() { cancel(); <-done }() // no worker runs the job, and the database closes after Run
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var queued int
		if err := db.QueryRow(`SELECT count(*) FROM jobs WHERE repository_id = 1 AND kind = 'index'`).Scan(&queued); err != nil {
			t.Fatal(err)
		}
		if queued > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the moved commit's index job was not queued without a sync")
		}
	}
}

// TestRunFillsTerms: Run gives the chunks an older version stored without
// terms theirs, beside the server.
func TestRunFillsTerms(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/acme/demo', 'acme', 'demo');
		INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'a');
		INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'a.go', 1, 1, 'getUserToken()', 'h')`); err != nil {
		t.Fatal(err)
	}
	log.SetOutput(io.Discard) // the new administrator's one-time password
	defer log.SetOutput(os.Stderr)
	a, err := New(ctx, Config{DataDir: dir}, db, http.NotFoundHandler(), true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var terms sql.NullString
		if err := db.QueryRow(`SELECT terms FROM chunks`).Scan(&terms); err != nil {
			t.Fatal(err)
		}
		if terms.String == "get user token" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the chunk's terms were not filled in: %v", terms)
		}
	}
}

// localRemote makes a git repository with one commit, and has git read it in
// place of https://github.com/acme/demo for the rest of the test.
func localRemote(t *testing.T) {
	remote := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "c"}} {
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
}

// TestProviders: the chat model is the one the settings name, with its
// context and the share of it a request may fill; none without a model.
func TestProviders(t *testing.T) {
	if chatP, embedP := Providers(Config{ChatModel: "m", ChatContext: 524288, CompactRatio: 0.5}); chatP == nil || chatP.Window != 524288 || chatP.CompactRatio != 0.5 || embedP != nil {
		t.Errorf("%+v %+v", chatP, embedP)
	}
	if chatP, _ := Providers(Config{ChatContext: 524288}); chatP != nil {
		t.Errorf("no model: %+v", chatP)
	}
}

// TestBadSettingCreatesNoAdmin: a setting that stops the start, a proxy that
// is no address or a chat tokenizer that cannot be read, is caught before the
// first administrator is created, so no one-time password is printed for a
// server that never ran.
func TestBadSettingCreatesNoAdmin(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, cfg := range []Config{{DataDir: dir, TrustedProxies: []string{"proxy.local"}}, {DataDir: dir, ChatModel: "m", ChatTokenizer: filepath.Join(dir, "none.json")}} {
		if _, err := New(ctx, cfg, db, http.NotFoundHandler(), true); err == nil || cfg.ChatModel != "" && !strings.Contains(err.Error(), "DEEPCODE_CHAT_TOKENIZER") {
			t.Fatalf("started with %+v: %v", cfg, err)
		}
	}
	var users int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("%d users after the failed start (%v)", users, err)
	}
}
