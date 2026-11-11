package git

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

// TestStallFollowsDeadline: git gives up on a stalled transfer after five
// minutes, or by the command's deadline when that comes first, so a remote
// helper never outlives a git killed at its deadline by long.
func TestStallFollowsDeadline(t *testing.T) {
	stall := func(ctx context.Context) string {
		env := (&Client{}).env(ctx, Remote{})
		for i := len(env) - 1; i >= 0; i-- { // the last value is the one git sees
			if v, ok := strings.CutPrefix(env[i], "GIT_HTTP_LOW_SPEED_TIME="); ok {
				return v
			}
		}
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if got := stall(ctx); got != "59" && got != "60" {
		t.Fatalf("under a one-minute deadline: %s seconds", got)
	}
	if got := stall(context.Background()); got != "300" {
		t.Fatalf("without a deadline: %s seconds", got)
	}
}

// TestTokenStaysScoped: git sees the Authorization header for the remote's
// URL only, appended after configuration the environment already passes, with
// redirects from that URL refused even where the operator's configuration
// follows them for its host, and neither deepcode's own settings nor a
// repository the environment points at.
func TestTokenStaysScoped(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "kept")
	t.Setenv("GIT_CONFIG_KEY_1", "http.https://git.example.com/.followRedirects") // the operator's, for the host
	t.Setenv("GIT_CONFIG_VALUE_1", "true")
	t.Setenv("DEEPCODE_CHAT_API_KEY", "secret")
	t.Setenv("Deepcode_Secret_Key", "secret")
	t.Setenv("GIT_DIR", t.TempDir()) // as inside a git hook
	env := (&Client{AllowProtocol: "https"}).env(context.Background(), Remote{URL: "https://git.example.com/group/app.git", Authorization: "Basic c2VjcmV0"})
	for _, kv := range env {
		if up := strings.ToUpper(kv); strings.HasPrefix(up, "DEEPCODE_") || strings.HasPrefix(up, "GIT_DIR=") {
			t.Fatalf("git would see %s", kv)
		}
	}
	config := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"config"}, args...)...)
		cmd.Env = env
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	header := func(url string) string { return config("--get-urlmatch", "http.extraheader", url) }
	if got := header("https://git.example.com/group/app.git/info/refs"); got != "Authorization: Basic c2VjcmV0" {
		t.Fatalf("header for the remote: %q", got)
	}
	if got := header("https://git.example.com/other/app.git/info/refs"); got != "" {
		t.Fatalf("header leaked to another repository: %q", got)
	}
	if got := config("user.name"); got != "kept" {
		t.Fatalf("existing configuration lost: %q", got)
	}
	if got := config("--get-urlmatch", "http.followredirects", "https://git.example.com/group/app.git/info/refs"); got != "false" {
		t.Fatalf("redirects are followed: %q", got)
	}
}

// TestProtocolsAllowed: git reaches remotes only over the allowed protocols.
func TestProtocolsAllowed(t *testing.T) {
	url, run := bare(t)
	run("update-ref", "HEAD", run("commit-tree", run("mktree"), "-m", "first"))
	g := &Client{Dir: t.TempDir(), AllowProtocol: "http:https"}
	if sha, err := g.LsRemote(context.Background(), Remote{URL: url}); err == nil {
		t.Fatalf("read a file:// remote with only http and https allowed: %s", sha)
	}
}

// TestLsTreeSkipsLinks: a symlink (whose blob is its target's path) and a
// submodule are not files to index.
func TestLsTreeSkipsLinks(t *testing.T) {
	dir := t.TempDir()
	git := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("", "init", "-q", "--bare")
	blob := git("hello", "hash-object", "-w", "--stdin")
	tree := git("100644 blob "+blob+"\tfile.txt\n120000 blob "+blob+"\tlink\n160000 commit "+strings.Repeat("1", 40)+"\tsub\n", "mktree")
	entries, err := (&Client{}).LsTree(context.Background(), dir, tree)
	if err != nil || len(entries) != 1 || entries[0].Path != "file.txt" || entries[0].Size != 5 {
		t.Fatalf("got %+v %v, want file.txt alone", entries, err)
	}
}

// bare makes a bare repository (git init plus initArgs) and returns its file
// URL and a runner for git commands in it.
func bare(t *testing.T, initArgs ...string) (string, func(...string) string) {
	dir := t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run(append([]string{"init", "-q", "--bare"}, initArgs...)...)
	url := filepath.ToSlash(dir)
	if !strings.HasPrefix(url, "/") {
		url = "/" + url
	}
	return "file://" + url, run
}

// TestEmptyRepository: a repository whose default branch has no commit is
// ErrEmpty, even when another ref's name ends in HEAD.
func TestEmptyRepository(t *testing.T) {
	url, run := bare(t)
	run("update-ref", "refs/remotes/origin/HEAD", run("commit-tree", run("mktree"), "-m", "elsewhere"))
	g := &Client{Dir: t.TempDir(), AllowProtocol: "file"}
	if _, err := g.LsRemote(context.Background(), Remote{URL: url}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty repository: %v", err)
	}
}

// TestSHA256: a SHA-256 repository is fetched into a SHA-256 clone.
func TestSHA256(t *testing.T) {
	url, run := bare(t, "--object-format=sha256")
	run("update-ref", "HEAD", run("commit-tree", run("mktree"), "-m", "first"))
	g := &Client{Dir: t.TempDir(), AllowProtocol: "file"}
	ctx := context.Background()
	sha, err := g.LsRemote(ctx, Remote{URL: url})
	if err != nil || len(sha) != 64 {
		t.Fatalf("ls-remote: %q %v", sha, err)
	}
	dir, err := g.Fetch(ctx, 1, Remote{URL: url}, sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.LsTree(ctx, dir, sha); err != nil {
		t.Fatal(err)
	}
}

// TestBlobsCloseAfterFailedRead: Close returns although a failed Read left a
// body larger than the pipe holds unread.
func TestBlobsCloseAfterFailedRead(t *testing.T) {
	_, run := bare(t)
	msg := filepath.Join(t.TempDir(), "message")
	if err := os.WriteFile(msg, []byte(strings.Repeat("a long commit message\n", 1<<16)), 0o600); err != nil {
		t.Fatal(err)
	}
	commit := run("commit-tree", run("mktree"), "-F", msg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // ends a git that Close left behind
	b, err := (&Client{}).OpenBlobs(ctx, run("rev-parse", "--absolute-git-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Read(commit); err == nil {
		t.Fatal("a commit was read as a blob")
	}
	done := make(chan error, 1)
	go func() { done <- b.Close() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close waits on a git that cannot finish writing")
	}
}

// TestHungServer: once the context ends, git returns even though the server
// never answers and git's remote helper still holds its pipes.
func TestHungServer(t *testing.T) {
	dir := t.TempDir() // made first, so it is removed after the cleanup below lets git's helper go
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var open []net.Conn // held, never answered
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range open {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			open = append(open, c)
			mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	url := "http://" + ln.Addr().String() + "/r.git"
	start := time.Now()
	if _, err := (&Client{Dir: dir, AllowProtocol: "http"}).run(ctx, Remote{URL: url}, "ls-remote", url, "HEAD"); err == nil {
		t.Fatal("a server that never answered was read")
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("git returned only after %v", d)
	}
}

// TestCancelEndsHelper: on Windows, cancelling a command ends git's whole
// process tree. The git on PATH is usually Git for Windows' launcher, whose
// child is the real git; killing the launcher alone left that git's remote
// helper holding (and downloading through) its connection after the size
// limit stopped a fetch. Elsewhere git itself is killed, and its helper ends
// at its next write or once the transfer stalls (env).
func TestCancelEndsHelper(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the launcher is Git for Windows'")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0") // takes the connection, never answers
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithCancel(context.Background()) // no deadline: no stall limit ends the helper first
	defer cancel()
	g := &Client{Dir: t.TempDir(), AllowProtocol: "http"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.run(ctx, Remote{}, "ls-remote", "http://"+ln.Addr().String()+"/r.git")
	}()
	var c net.Conn
	select {
	case c = <-accepted:
		defer c.Close()
	case <-done:
		t.Fatal("git ended before its helper connected")
	case <-time.After(30 * time.Second):
		t.Fatal("git's helper never connected")
	}
	cancel()
	<-done
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(io.Discard, c); errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("git's helper still holds its connection after the command was cancelled")
	}
}

// TestErrorOneLine: git's stderr ends up in the error as one line with no
// control characters, whatever the remote or an argument put in it.
func TestErrorOneLine(t *testing.T) {
	_, run := bare(t)
	_, err := (&Client{Dir: run("rev-parse", "--absolute-git-dir")}).run(context.Background(), Remote{}, "cat-file", "-t", "forged\nline\x1b[31m")
	if err == nil || strings.ContainsFunc(err.Error(), unicode.IsControl) || !strings.Contains(err.Error(), "forged") {
		t.Fatalf("%q", err)
	}
}

// TestOutputLimit: a command that prints more than maxOutput fails instead
// of being held whole.
func TestOutputLimit(t *testing.T) {
	g := &Client{Dir: t.TempDir()}
	ctx := context.Background()
	if out, err := g.run(ctx, Remote{}, "version"); err != nil || !strings.HasPrefix(string(out), "git version") {
		t.Fatalf("under the limit: %q %v", out, err)
	}
	maxOutput = 4
	t.Cleanup(func() { maxOutput = 64 << 20 })
	if _, err := g.run(ctx, Remote{}, "version"); err == nil || !strings.Contains(err.Error(), "output passed") {
		t.Fatalf("over the limit: %v", err)
	}
}

// TestFetchSizeLimit: a download that adds more than MaxBytes to the clone
// fails with ErrTooLarge and leaves no clone behind, even one that finishes
// before its size is first looked at; under a higher limit it is kept.
func TestFetchSizeLimit(t *testing.T) {
	measureEvery = time.Hour // only the look at the end sees this download
	t.Cleanup(func() { measureEvery = 250 * time.Millisecond })
	url, run := bare(t)
	noise := make([]byte, 2<<20)
	rand.Read(noise)
	msg := filepath.Join(t.TempDir(), "message")
	if err := os.WriteFile(msg, []byte(base64.StdEncoding.EncodeToString(noise)), 0o600); err != nil { // random: it does not compress
		t.Fatal(err)
	}
	sha := run("commit-tree", run("mktree"), "-F", msg)
	run("update-ref", "HEAD", sha)
	ctx := context.Background()
	g := &Client{Dir: t.TempDir(), AllowProtocol: "file", MaxBytes: 1 << 20}
	if _, err := g.Fetch(ctx, 1, Remote{URL: url}, sha); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a 2 MB download under a 1 MB limit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Dir, "1.git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the clone was kept: %v", err)
	}
	g.MaxBytes = 8 << 20
	if _, err := g.Fetch(ctx, 1, Remote{URL: url}, sha); err != nil {
		t.Fatalf("under an 8 MB limit: %v", err)
	}
}

// TestWatch: once the files under a directory pass the limit, the watch stops
// the context with ErrTooLarge, while the download still runs.
func TestWatch(t *testing.T) {
	dir := t.TempDir()
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	go watch(ctx, stop, dir, 1000, 10*time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "tmp_pack"), make([]byte, 2000), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("a directory past its limit was not stopped")
	}
	if !errors.Is(context.Cause(ctx), ErrTooLarge) {
		t.Fatalf("stopped by %v", context.Cause(ctx))
	}
}

// TestFetchKeepsOneCommit: fetching another commit starts the clone over, so
// older trees do not pile up in it; fetching the commit it holds keeps it, and
// so does a fetch stopped before it began.
func TestFetchKeepsOneCommit(t *testing.T) {
	url, run := bare(t)
	tree := run("mktree")
	first, second := run("commit-tree", tree, "-m", "first"), run("commit-tree", tree, "-m", "second")
	run("update-ref", "refs/heads/first", first)
	run("update-ref", "refs/heads/second", second)
	g := &Client{Dir: t.TempDir(), AllowProtocol: "file"}
	ctx := context.Background()
	for _, sha := range []string{first, second} {
		if _, err := g.Fetch(ctx, 1, Remote{URL: url}, sha); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(g.Dir, "1.git")
	if exec.Command("git", "--git-dir="+dir, "cat-file", "-e", first).Run() == nil {
		t.Fatal("the clone kept the first commit")
	}
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Fetch(ctx, 1, Remote{URL: url}, second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fetching the commit it holds started the clone over: %v", err)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.Fetch(stopped, 1, Remote{URL: url}, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("a stopped fetch: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a stopped fetch removed the clone: %v", err)
	}
}

// TestValidBranch: a name git can check out, and the ones it cannot.
func TestValidBranch(t *testing.T) {
	g := &Client{}
	ctx := context.Background()
	for _, good := range []string{"main", "feature/x", "v1.2"} {
		if !g.ValidBranch(ctx, good) {
			t.Errorf("refused %q", good)
		}
	}
	for _, bad := range []string{"", "-x", "a..b", "a b", "x.lock", "a~b", "a^b", "a:b", "a?b", "a[b", "a\\b", "a//b"} {
		if g.ValidBranch(ctx, bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}
