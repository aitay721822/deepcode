// Package acpprobe is P0 of data/design-v2-acp.md: it drives a real ACP
// agent the way deepcode's ACP backend would, as a plain model, and records
// what the agent does. Its test runs only when DEEPCODE_PROBE=ACP.
package acpprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"deepcode/internal/knowledge/chat/harness"
)

// steps are the steps a run may ask for, in the order they run. S1, starting
// and stopping the agent, always runs; S4 is read from every step's usage and
// S8 from the adapters' source, so neither is a step here.
var steps = []string{"S2", "S3", "S5", "S6", "S7", "S9", "S10", "S11"}

// TestACP probes one agent. DEEPCODE_PROBE_AGENT is its command line, split
// at spaces; DEEPCODE_PROBE_STEPS, which of steps to run, comma separated;
// DEEPCODE_PROBE_DIR, where the findings go as JSON lines (acp-<run>.jsonl),
// with each agent's stderr and the sessions' folders; DEEPCODE_PROBE_ENV,
// variables the agent gets besides the passed ones (NAME=value, separated by
// semicolons; their names are recorded, never their values). Run it with
// -timeout 120m. It sends some 60 small prompts through the agent's own
// sign-in, and fails only when the run itself breaks.
func TestACP(t *testing.T) {
	if os.Getenv("DEEPCODE_PROBE") != "ACP" {
		t.Skip("set DEEPCODE_PROBE=ACP to probe an ACP agent")
	}
	line, given := strings.TrimSpace(os.Getenv("DEEPCODE_PROBE_AGENT")), os.Getenv("DEEPCODE_PROBE_DIR")
	dir, err := filepath.Abs(given)
	if line == "" || given == "" || err != nil {
		t.Fatal("set DEEPCODE_PROBE_AGENT and DEEPCODE_PROBE_DIR")
	}
	var run []string
	for _, s := range strings.Split(os.Getenv("DEEPCODE_PROBE_STEPS"), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if !slices.Contains(steps, s) {
			t.Fatalf("unknown step %q: the steps are %v", s, steps)
		}
		run = append(run, s)
	}
	var extra, names []string
	for i, kv := range strings.Split(os.Getenv("DEEPCODE_PROBE_ENV"), ";") {
		if kv = strings.TrimSpace(kv); kv == "" {
			continue
		}
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			t.Fatalf("DEEPCODE_PROBE_ENV: entry %d is not NAME=value", i+1) // never the entry: it may be a secret
		}
		extra, names = append(extra, kv), append(names, name)
	}
	tokens, err := harness.LoadTokens("")
	if err != nil {
		t.Fatal(err)
	}
	id := time.Now().UTC().Format("20060102T150405Z")
	out, err := os.Create(filepath.Join(dir, "acp-"+id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() }) // first registered, so it runs after every agent's stop
	p := &prober{t: t, run: id, dir: dir, out: json.NewEncoder(out), tokens: tokens.Count, work: filepath.Join(dir, "work", id)}
	m := machine(line, run)
	m["env"] = names
	p.emit("machine", m)
	if slices.Contains(run, "S2") {
		p.unsigned(line)
	}
	a := p.start(line, extra)
	for _, s := range run {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		switch s {
		case "S3":
			p.tools(ctx, a)
		case "S5":
			p.json(ctx, a)
		case "S6":
			p.gather(ctx, a)
		case "S7":
			p.parallel(ctx, a)
		case "S9":
			p.sessions(ctx, a)
		case "S10":
			p.scale(ctx, a)
		case "S11":
			p.isolation(ctx, a)
		}
		cancel()
	}
	p.stop(a)
}

// prober is one run: where its findings go, how text is counted (o200k), and
// which adapter's own options a session gets (meta).
type prober struct {
	t      *testing.T
	run    string // the run's id, on every finding and file
	dir    string
	work   string // the sessions' folders
	mu     sync.Mutex
	out    *json.Encoder
	tokens func(string) int
	kind   string // "claude", "codex" or "other" (kindOf)
	agents int    // agents launched: names each one's stderr file
}

// emit writes one finding as a JSON line, and to the test log cut short. It
// is safe for use by several goroutines.
func (p *prober) emit(kind string, v any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.t.Logf("%s: %s", kind, cut(mustJSON(v), 2000))
	if err := p.out.Encode(map[string]any{"run": p.run, "at": time.Now().UTC().Format(time.RFC3339Nano), "kind": kind, "v": v}); err != nil {
		p.t.Errorf("emit %s: %v", kind, err)
	}
}

// mustJSON is v as JSON, or the error that kept it from being.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

// cut is s cut to at most n bytes.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// machine is what the run ran on: the OS, the agent's command as resolved,
// and the versions of node and the CLIs the adapters drive.
func machine(line string, run []string) map[string]any {
	version := func(name string, args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			return "error: " + err.Error()
		}
		return strings.TrimSpace(string(out))
	}
	resolved, err := exec.LookPath(strings.Fields(line)[0])
	if err != nil {
		resolved = "error: " + err.Error()
	}
	return map[string]any{"os": runtime.GOOS + "/" + runtime.GOARCH, "line": line, "resolved": resolved, "steps": run,
		"node": version("node", "--version"), "claude": version("claude", "--version"), "codex": version("codex", "--version")}
}

// passed are the variables an agent gets from this process: what Windows,
// Node and npm need, and proxies. Everything else stays out: the keys of
// deepcode and of other services (DEEPCODE_*, DS_APIKEY, OPENAI_API_KEY, which
// would also bill an API instead of the subscription) and what a Claude Code
// host sets (CLAUDE*, ANTHROPIC_*), so the agent signs in as its own CLI does;
// a login kept under a CLAUDE_CONFIG_DIR or CODEX_HOME of the user's own
// therefore reads as signed out. Git Bash's path, which Claude Code on
// Windows may need, and npm's own settings (NPM_CONFIG_*) pass.
var passed = []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "TEMP", "TMP", "USERPROFILE", "HOMEDRIVE",
	"HOMEPATH", "HOME", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "ALLUSERSPROFILE", "PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432",
	"COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432", "PUBLIC", "USERNAME", "USERDOMAIN", "COMPUTERNAME", "OS",
	"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "LANG", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "NODE_EXTRA_CA_CERTS",
	"SSL_CERT_FILE", "CLAUDE_CODE_GIT_BASH_PATH"}

// env is the passed variables of this process's environment, then extra.
func env(extra []string) []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name = strings.ToUpper(name); slices.Contains(passed, name) || strings.HasPrefix(name, "NPM_CONFIG_") {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

// air declares the two extensions the Claude and Codex adapters share (their
// "AIR" metadata): failures as typed session updates rather than text in the
// answer, and custom instructions on session/new.
var air = map[string]any{"jetbrains": map[string]any{"air": map[string]any{"version": 1,
	"capabilities": []string{"sessionFailure", "customInstructions"}}}}

// launch starts the agent of line with env(extra), its stderr in
// <dir>/stderr-<run>-<n>.log, and initializes it. Once started, the agent is
// stopped when the test ends, whatever happens; an agent that failed to
// initialize is returned with the error.
func (p *prober) launch(line string, extra []string, label string) (*agent, error) {
	f := strings.Fields(line)
	p.agents++
	stderr, err := os.Create(filepath.Join(p.dir, fmt.Sprintf("stderr-%s-%d.log", p.run, p.agents)))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(f[0], f[1:]...)
	cmd.Env, cmd.Stderr, cmd.WaitDelay = env(extra), stderr, 5*time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stderr.Close()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stderr.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stderr.Close()
		return nil, err
	}
	a := &agent{label: label, cmd: cmd, stdin: stdin, stderr: stderr, rec: &recorder{by: map[acp.SessionId]*turn{}}}
	a.conn = acp.NewClientSideConnection(a.rec, stdin, stdout)
	p.t.Cleanup(func() { p.stop(a) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a.init, err = a.conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{Meta: air}})
	return a, err
}

// kindOf is which adapter a is: by the name it gave, else by its command line.
func kindOf(line string, a *agent) string {
	name := strings.ToLower(line)
	if a.init.AgentInfo != nil {
		name = strings.ToLower(a.init.AgentInfo.Name)
	}
	switch {
	case strings.Contains(name, "claude"):
		return "claude"
	case strings.Contains(name, "codex"):
		return "codex"
	}
	return "other"
}

// start is S1: the agent launched with extra and initialized, and what it
// says it is.
func (p *prober) start(line string, extra []string) *agent {
	began := time.Now()
	a, err := p.launch(line, extra, "S1")
	if err != nil {
		p.t.Fatalf("start %s: %v", line, err)
	}
	p.kind = kindOf(line, a)
	if strings.Contains(line, "claude") && p.kind != "claude" || strings.Contains(line, "codex") && p.kind != "codex" {
		p.t.Errorf("the agent of %q says it is %+v: its own options are not sent", line, a.init.AgentInfo)
	}
	p.emit("S1.init", map[string]any{"ms": time.Since(began).Milliseconds(), "kind": p.kind, "protocol": a.init.ProtocolVersion,
		"agent": a.init.AgentInfo, "capabilities": a.init.AgentCapabilities, "auth": a.init.AuthMethods, "meta": a.init.Meta})
	return a
}

// stop ends a, once: it closes stdin and lets the agent end on its own for
// 5 s, kills it, and 3 s later kills any of its processes still running (the
// same pid, name and start time), saying what it found.
func (p *prober) stop(a *agent) {
	a.once.Do(func() {
		ours := tree(p.t, a.cmd.Process.Pid)
		_ = a.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- a.cmd.Wait() }()
		exited := true
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			exited = false
			_ = a.cmd.Process.Kill()
			<-done
		}
		time.Sleep(3 * time.Second)
		now := processes(p.t)
		var left []string
		for pid, pr := range ours {
			if still, ok := now[pid]; ok && still.Name == pr.Name && still.Created == pr.Created {
				left = append(left, fmt.Sprintf("%d %s", pid, pr.Name))
				_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F").Run()
			}
		}
		a.stderr.Close()
		p.emit(a.label+".stop", map[string]any{"exitedOnEOF": exited, "processes": ours, "left": left})
	})
}

// proc is a running process as Win32_Process lists it; Created is its start,
// as a Windows file time.
type proc struct {
	Name    string
	Parent  int
	KB      int
	Created int64
}

// processes lists the running processes by pid (Windows), or nil if the
// listing fails or takes over 30 s.
func processes(t *testing.T) map[int]proc {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
		"ConvertTo-Json -Compress -InputObject @(Get-CimInstance Win32_Process | "+
			"Select-Object ProcessId,ParentProcessId,Name,WorkingSetSize,@{n='Created';e={$_.CreationDate.ToFileTimeUtc()}})").Output()
	if err != nil {
		t.Logf("processes: %v", err)
		return nil
	}
	var rows []struct {
		ProcessId, ParentProcessId int
		Name                       string
		WorkingSetSize, Created    int64
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Logf("processes: %v", err)
		return nil
	}
	m := map[int]proc{}
	for _, r := range rows {
		m[r.ProcessId] = proc{Name: r.Name, Parent: r.ParentProcessId, KB: int(r.WorkingSetSize >> 10), Created: r.Created}
	}
	return m
}

// tree is root and the processes it started, and theirs, running now: a
// process joins when its parent did and it started after that parent, so a
// pid reused since never pulls in another program's.
func tree(t *testing.T, root int) map[int]proc {
	all, out := processes(t), map[int]proc{}
	if pr, ok := all[root]; ok {
		out[root] = pr
	}
	for grew := true; grew; {
		grew = false
		for pid, pr := range all {
			parent, in := out[pr.Parent]
			if _, seen := out[pid]; in && !seen && pid != pr.Parent && pr.Created >= parent.Created {
				out[pid], grew = pr, true
			}
		}
	}
	return out
}

// memory is the working sets of m, in KB.
func memory(m map[int]proc) (kb int) {
	for _, p := range m {
		kb += p.KB
	}
	return kb
}
