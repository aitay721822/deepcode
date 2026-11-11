// Package git reads repositories with the system git.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ErrEmpty is a repository whose default branch has no commit to index.
var ErrEmpty = errors.New("Repository 的預設 Branch 沒有任何 Commit")

// ErrNoBranch is a branch the remote does not have, or one with no commit.
var ErrNoBranch = errors.New("Branch 不存在或沒有任何 Commit")

// Client runs the system git with fixed arguments (never through a shell).
// Each repository is a bare, shallow clone: files are read from the object
// store by blob SHA, so there is no working tree to be affected by autocrlf,
// LFS filters, symlinks or case-folding filesystems.
type Client struct {
	Dir           string // holds one bare repository per imported repo
	AllowProtocol string // GIT_ALLOW_PROTOCOL; "http:https" in production
	MaxBytes      int64  // what one download may add to a clone; 0 for no limit
}

// Remote is a repository to fetch: its clone URL and, for a server with an
// access token, the Authorization header to send it.
type Remote struct {
	URL           string
	Authorization string // a secret; "" for anonymous access
}

type TreeEntry struct {
	Path string
	SHA  string
	Size int64
}

// command is git with args, ended with ctx. On Windows the git on PATH is
// usually Git for Windows' launcher (cmd\git.exe), which runs the real git as
// its child: killing the launcher alone leaves that git and its remote helper
// downloading past a deadline or the size limit. So the whole tree goes, or
// should taskkill fail, the launcher at least.
func command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	if runtime.GOOS == "windows" {
		cmd.Cancel = func() error {
			if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
	}
	return cmd
}

func (g *Client) run(ctx context.Context, auth Remote, args ...string) ([]byte, error) {
	cmd := command(ctx, args...)
	cmd.Dir = g.Dir
	cmd.Env = g.env(ctx, auth)
	cmd.WaitDelay = 5 * time.Second // git's remote helpers hold its pipes; once git is killed, stop waiting for them
	var stdout capped
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if stdout.over {
			return nil, fmt.Errorf("git %s: its output passed %d MB", args[0], maxOutput>>20)
		}
		// stderr carries the remote's own "remote:" lines: one line with no
		// control characters, so it cannot forge lines in the server log.
		msg := strings.Join(strings.FieldsFunc(stderr.String(), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return stdout.buf.Bytes(), nil
}

// maxOutput bounds what run holds of one command's output: ls-tree lists a
// tree of a million empty files in about a hundred megabytes, though it
// downloads in a few. A test lowers it.
var maxOutput = 64 << 20

// capped is an output that fails once it would pass maxOutput; the failed
// write closes git's pipe, so git stops too. It holds a Buffer rather than
// embedding one, whose ReadFrom would let io.Copy go round Write.
type capped struct {
	buf  bytes.Buffer
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > maxOutput {
		c.over = true
		return 0, errors.New("past the output limit")
	}
	return c.buf.Write(p)
}

// repoLocal are the variables that point git at a repository, as a git hook
// sets them: deepcode names each repository itself, and a fetch would
// otherwise land in that one.
var repoLocal = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR"}

// env never lets git prompt, allows only the configured protocols, gives up
// on a transfer stalled for five minutes (a big repository's server may say
// nothing while it packs), or until ctx's deadline if that comes first, and
// leaves out deepcode's own settings (keys and passwords) and repoLocal. A
// remote's Authorization header travels as configuration in the environment,
// not on the command line where other users could read it; it is scoped to the
// remote's URL, and redirects from that URL are refused as specifically, so an
// operator's own followRedirects for its host cannot take git elsewhere.
func (g *Client) env(ctx context.Context, auth Remote) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(strings.ToUpper(kv), "=")
		if !strings.HasPrefix(name, "DEEPCODE_") && !slices.Contains(repoLocal, name) {
			env = append(env, kv)
		}
	}
	stall := 5 * time.Minute
	if d, ok := ctx.Deadline(); ok {
		stall = min(stall, time.Until(d)) // killing git at the deadline leaves its remote helper running until the transfer stalls
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=never",
		"GIT_ALLOW_PROTOCOL="+g.AllowProtocol,
		"GIT_HTTP_LOW_SPEED_LIMIT=1", fmt.Sprintf("GIT_HTTP_LOW_SPEED_TIME=%d", max(1, int(stall.Seconds())))) // a stalled transfer ends by itself, helper and all
	if auth.Authorization == "" {
		return env
	}
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT")) // after any configuration the environment already holds
	return append(env,
		fmt.Sprintf("GIT_CONFIG_KEY_%d=http.%s.extraHeader", n, auth.URL),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=Authorization: %s", n, auth.Authorization),
		fmt.Sprintf("GIT_CONFIG_KEY_%d=http.%s.followRedirects", n+1, auth.URL),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=false", n+1),
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", n+2))
}

// LsRemote returns the commit SHA the remote's HEAD points at.
func (g *Client) LsRemote(ctx context.Context, remote Remote) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute) // one line to read; a hung server must not hold the request
	defer cancel()
	out, err := g.run(ctx, remote, "-c", "credential.helper=", "ls-remote", remote.URL, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") { // the pattern also matches refs ending in /HEAD
		sha, ref, _ := strings.Cut(line, "\t")
		// SHA-1 or SHA-256 in hex: the server's answer goes on git's command lines.
		if ref == "HEAD" && (len(sha) == 40 || len(sha) == 64) && strings.Trim(sha, "0123456789abcdef") == "" {
			return sha, nil
		}
	}
	return "", ErrEmpty // no HEAD line when there is no commit
}

// LsRemoteRef returns the commit SHA the remote's branch points at.
func (g *Client) LsRemoteRef(ctx context.Context, remote Remote, branch string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	ref := "refs/heads/" + branch
	out, err := g.run(ctx, remote, "-c", "credential.helper=", "ls-remote", remote.URL, ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		sha, got, _ := strings.Cut(line, "\t")
		if got == ref && (len(sha) == 40 || len(sha) == 64) && strings.Trim(sha, "0123456789abcdef") == "" {
			return sha, nil
		}
	}
	return "", ErrNoBranch
}

// Branches returns the remote's branches, their names only, sorted.
func (g *Client) Branches(ctx context.Context, remote Remote) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := g.run(ctx, remote, "-c", "credential.helper=", "ls-remote", "--heads", remote.URL)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		_, ref, _ := strings.Cut(line, "\t")
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok && name != "" {
			names = append(names, name)
		}
	}
	if names == nil {
		names = []string{}
	}
	slices.Sort(names)
	return names, nil
}

// ValidBranch reports whether name is a branch git can check out: no leading
// dash, and every rule check-ref-format states.
func (g *Client) ValidBranch(ctx context.Context, name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " ~^:?*[\\\x00") {
		return false
	}
	_, err := g.run(ctx, Remote{}, "check-ref-format", "--branch", name)
	return err == nil
}

// Fetch downloads exactly one commit (depth 1) into the repo's bare clone.
// The clone holds that commit only, so syncs do not pile trees up in it: one
// without the commit starts over, since a depth-1 download is the whole tree
// anyway, and incremental indexing compares blob SHAs in the database, not with
// older commits here. A download that adds more than MaxBytes fails with
// ErrTooLarge: it is stopped once seen (its size is looked at four times a
// second, and once more at the end), and the clone is removed.
func (g *Client) Fetch(ctx context.Context, repoID int64, remote Remote, sha string) (string, error) {
	dir, err := filepath.Abs(filepath.Join(g.Dir, strconv.FormatInt(repoID, 10)+".git"))
	if err != nil {
		return "", err
	}
	// --git-dir, not -C: a broken clone must not lead git up to a repository around the data folder.
	if _, err := g.run(ctx, Remote{}, "--git-dir="+dir, "cat-file", "-e", sha+"^{commit}"); err != nil { // new, another commit, or cut short
		if ctx.Err() != nil {
			return "", ctx.Err() // stopped, which says nothing about the clone
		}
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
		args := []string{"init", "--bare", "-q"}
		if len(sha) == 64 {
			args = append(args, "--object-format=sha256")
		}
		if _, err := g.run(ctx, Remote{}, append(args, dir)...); err != nil {
			return "", err
		}
	}
	fetch, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	limit := diskUsage(dir) + g.MaxBytes
	if g.MaxBytes > 0 {
		go watch(fetch, stop, dir, limit, measureEvery)
	}
	// No maintenance inside the fetch (gc.auto before git 2.29): a repack would
	// count as download, and a clone of one commit needs none.
	_, err = g.run(fetch, remote, "-C", dir, "-c", "credential.helper=", "-c", "maintenance.auto=false", "-c", "gc.auto=0",
		"fetch", "-q", "--depth=1", "--no-tags", remote.URL, sha)
	if g.MaxBytes > 0 && err == nil && diskUsage(dir) > limit {
		stop(ErrTooLarge) // it finished before the watch looked
	}
	if errors.Is(context.Cause(fetch), ErrTooLarge) {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("git: remove %s after a download over the limit: %v", dir, err) // the next fetch starts over anyway
		}
		return "", fmt.Errorf("%w of %d MB (DEEPCODE_MAX_REPO_MB)", ErrTooLarge, g.MaxBytes>>20)
	}
	return dir, err
}

// LsTree lists the regular files at sha with their blob SHA and size.
// Symlinks and submodules are skipped.
func (g *Client) LsTree(ctx context.Context, dir, sha string) ([]TreeEntry, error) {
	out, err := g.run(ctx, Remote{}, "-C", dir, "ls-tree", "-r", "-l", "-z", sha)
	if err != nil {
		return nil, err
	}
	var entries []TreeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		f := strings.Fields(string(meta)) // mode type object size
		if !ok || len(f) != 4 || f[1] != "blob" || f[0] == "120000" {
			continue
		}
		size, _ := strconv.ParseInt(f[3], 10, 64)
		entries = append(entries, TreeEntry{Path: string(path), SHA: f[2], Size: size})
	}
	return entries, nil
}
