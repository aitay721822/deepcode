package acpprobe

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// parallel is S7: four requests at a time, each in its own session.
func (p *prober) parallel(ctx context.Context, a *agent) {
	began := time.Now()
	var wg sync.WaitGroup
	results := make([]result, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = p.askIn(ctx, a, "S7", fmt.Sprintf("parallel-%d", i+1), p.folder("S7", fmt.Sprint(i)), "Reply in one word.", "Reply with OK.", false)
		}()
	}
	wg.Wait()
	p.emit("S7", map[string]any{"wallMs": time.Since(began).Milliseconds(), "results": results})
}

// sessions is S9: 200 sessions opened and closed with no prompt, the agent's
// processes' memory sampled every 50.
func (p *prober) sessions(ctx context.Context, a *agent) {
	const n = 200
	kbs := []int{memory(tree(p.t, a.cmd.Process.Pid))}
	began, opened, failed := time.Now(), 0, []string{}
	for i := 1; i <= n && ctx.Err() == nil; i++ {
		s, _, err := p.open(ctx, a, p.folder("S9"), "Reply in one word.", false)
		if err != nil {
			failed = append(failed, cut(err.Error(), 200))
			continue
		}
		opened++
		p.close(a, s.SessionId)
		if i%50 == 0 {
			kbs = append(kbs, memory(tree(p.t, a.cmd.Process.Pid)))
		}
	}
	p.emit("S9", map[string]any{"close": a.init.AgentCapabilities.SessionCapabilities.Close != nil, "asked": n, "opened": opened,
		"failed": len(failed), "errors": failed[:min(len(failed), 5)], "msEach": time.Since(began).Milliseconds() / int64(max(opened, 1)),
		"agentKB": kbs, "processes": tree(p.t, a.cmd.Process.Pid)})
}

// samples are S10's texts, of the kinds deepcode sends: code in four
// languages and Traditional Chinese prose, from this repository.
var samples = []string{
	"internal/platform/ai/client.go",
	"internal/knowledge/chat/loop/loop.go",
	"web/src/pages/admin/overview/UsageSection.tsx",
	"internal/platform/storage/migrations/001_init.sql",
	"README.md",
}

// scale is S10: in one session, a short prompt and then one sample a turn;
// after each turn, the context the agent reports against o200k's count of
// all the session sent and got, so each step's ratio and the overhead read
// off the pairs; reported says whether the turn had a usage update at all.
func (p *prober) scale(ctx context.Context, a *agent) {
	const static = "Reply in one word."
	s, _, err := p.open(ctx, a, p.folder("S10"), static, false)
	if err != nil {
		p.emit("S10", map[string]any{"error": err.Error()})
		return
	}
	defer p.close(a, s.SessionId)
	counted := 0
	if p.carries(false) {
		counted = p.tokens(static)
	}
	var pairs []map[string]any
	turn := func(name, text string) {
		r := p.send(ctx, a, s.SessionId, text)
		counted += r.SentTokens + r.ReplyTokens
		used, size := 0, 0
		if len(r.Updates) > 0 {
			used, size = r.Updates[len(r.Updates)-1].Used, r.Updates[len(r.Updates)-1].Size
		}
		pairs = append(pairs, map[string]any{"name": name, "reported": len(r.Updates) > 0, "used": used, "size": size, "o200k": counted, "usage": r.Usage,
			"thought": r.Thought, "other": r.Other, "ms": r.TotalMs, "err": r.Err})
	}
	turn("ok", p.text(static, "Reply with OK.", false))
	for _, path := range samples {
		code, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(path)))
		if err != nil {
			p.t.Errorf("S10 sample %s: %v", path, err)
			continue
		}
		turn(path, fmt.Sprintf("<source path=%q>\n%s\n</source>\n\nReply with OK.", path, code))
	}
	p.emit("S10", pairs)
}

// sessionRoots are where the agents keep their sessions' files.
func sessionRoots() []string {
	home, _ := os.UserHomeDir()
	return []string{filepath.Join(home, ".claude", "projects"), filepath.Join(home, ".codex", "sessions")}
}

// files are the files under the session roots, by path, with their
// modification times.
func files() map[string]time.Time {
	out := map[string]time.Time{}
	for _, root := range sessionRoots() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, err := d.Info(); err == nil {
					out[path] = info.ModTime()
				}
			}
			return nil
		})
	}
	return out
}

// isolation is S11: a session told a code word in one repository's folder,
// then one in another's asked for it; whether the agent's session list
// filtered by the second folder shows the first; and which session files
// written meanwhile name either folder. Claude's adapter names a session's
// folder in its file's path, every character but letters and digits a dash;
// Codex's files are by date and name the folder near their start, so the
// first 256 KB of those alone are searched, and never logged. Files naming
// neither folder (the user's own sessions meanwhile) are only counted.
func (p *prober) isolation(ctx context.Context, a *agent) {
	const static = "Reply briefly."
	before := files()
	repoA, repoB := p.folder("S11", "repo-a"), p.folder("S11", "repo-b")
	told := p.askIn(ctx, a, "S11", "tell", repoA, static, "Remember this code word: PELICAN-7. Reply with OK.", false)
	asked := p.askIn(ctx, a, "S11", "ask", repoB, static, "What code word were you told before this message? If none, reply NONE.", false)
	listed := map[string]any{}
	for name, cwd := range map[string]*string{"all": nil, "repo-b": &repoB} {
		var cwds []string
		var cursor *string
		for page := 0; page < 50; page++ {
			l, err := a.conn.ListSessions(ctx, acp.ListSessionsRequest{Cwd: cwd, Cursor: cursor})
			if err != nil {
				cwds = append(cwds, "error: "+err.Error())
				break
			}
			for _, s := range l.Sessions {
				if c := filepath.Clean(s.Cwd); len(c) >= len(p.work) && strings.EqualFold(c[:len(p.work)], p.work) {
					cwds = append(cwds, s.Cwd)
				}
			}
			if cursor = l.NextCursor; cursor == nil || *cursor == "" {
				break
			}
		}
		listed[name] = cwds
	}
	var kept []map[string]any
	others := 0
	for path, at := range files() {
		if old, ok := before[path]; ok && !at.After(old) {
			continue
		}
		head := ""
		if strings.HasPrefix(path, sessionRoots()[1]) {
			head = start(path, 256<<10)
		}
		f, named := map[string]any{"path": path}, false
		for name, dir := range map[string]string{"repoA": repoA, "repoB": repoB} {
			in := strings.Contains(path, nonAlnum.ReplaceAllString(dir, "-")) || strings.Contains(head, dir) ||
				strings.Contains(head, strings.ReplaceAll(dir, `\`, `\\`))
			f[name], named = in, named || in
		}
		if !named {
			others++
			continue
		}
		kept = append(kept, f)
	}
	p.emit("S11", map[string]any{"tell": told, "ask": asked, "leaked": strings.Contains(asked.Text, "PELICAN"), "listed": listed,
		"kept": kept, "others": others})
}

// nonAlnum is what Claude's adapter turns into dashes in a session folder's name.
var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// start is the first n bytes of the file at path, or "" if it cannot be read.
func start(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, n))
	return string(b)
}
