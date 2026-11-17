// Package acp is an ai.Chat on an agent that speaks the Agent Client Protocol
// (Claude Code, Codex, Gemini CLI, ...) on its standard input and output, on
// the subscription its user signed it in with (design 5). One agent process
// serves every request, started when the first needs it, and every request
// is a session of its own in an empty folder of its repository's, so nothing
// of one project reaches another's. deepcode's instructions take the place of
// the agent's own where its adapter takes them (Claude's system prompt,
// Codex's custom instructions), and head the prompt otherwise; the agent gets
// no tool, file or terminal of deepcode's, and none of the user's own
// settings unless UserConfig says so. Its usage is the agent's count, or the
// local tokenizer's estimate when it gives none.
package acp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/acp/proc"
	"deepcode/internal/platform/ai/usage"
)

// Agent is a chat model reached through an ACP agent's process. Its exported
// fields are its settings, fixed once it is used.
type Agent struct {
	Command    string // the agent's command line (acp_command)
	Dir        string // where it works: a folder per repository under work/, and its log
	Sessions   int    // prompts at a time; 0 is 1
	Model      string // the value of the agent's model option; "" keeps its default
	Effort     string // the value of its thought_level option; "" keeps its default
	CodexHome  string // CODEX_HOME for the agent; "" leaves Codex its own
	Connectors bool   // Claude: the account's claude.ai connectors join each session
	UserConfig bool   // the user's own settings, instructions, auto memory, MCP servers, plugins and skills join each session
	// Context is the context in tokens, 0 taking the agent's word for it;
	// CompactRatio is ai.Window's Ratio.
	Context      int
	CompactRatio float64
	Timeout      time.Duration    // per request
	RPM          int              // requests a minute, 0 unlimited
	Meter        usage.Meter      // told every request's Usage
	Count        func(string) int // the local tokenizer: estimates, Overhead and Scale; nil measures none

	once    sync.Once
	warned  sync.Once     // of the settings the agent leaves unused (warn)
	slots   chan struct{} // a prompt's turn (Sessions)
	lock    chan struct{} // held to pick or start the process
	turns   ai.Throttle
	mu      sync.Mutex // guards what follows
	now     *running
	live    map[*running]bool // every process started and not stopped: now, one starting, retired ones still serving
	fails   int               // starts failed since the agent last answered (note)
	retry   time.Time         // when the next start may be tried
	last    error             // the last start's failure, which requests get till retry
	closed  bool
	learned int                 // a context an agent refused a request by
	size    int                 // the context the agent reported
	probed  bool                // Overhead and Scale were measured
	over    int                 // Overhead
	scales  []float64           // the Scales the last requests showed
	info    *acp.Implementation // the name and version the agent gave, nil when it gave none
	model   string              // the value of its model option
	trouble error               // the last failure since the agent last answered
	at      time.Time           // trouble's
}

// running is an agent's process, its connection, and what it said it is.
type running struct {
	p        *proc.Process
	conn     *acp.ClientSideConnection
	cl       *client
	stderr   *os.File
	kind     string // claude, codex, or "" for an adapter deepcode has no options for
	closes   bool   // it offers session/close
	open     int    // sessions open now
	opened   int    // sessions opened in all
	retired  bool   // to end once its last session closes
	answered bool   // a prompt of it was answered
}

// recycle is the sessions an agent's process opens before another takes its
// place: Codex's grows by some megabytes a session and closing one frees
// none (design 5.1).
const recycle = 200

// grace is how long an agent has to end once its input closes, or to end a
// turn it was told to cancel (a variable, which tests shorten).
var grace = 5 * time.Second

// startLimit is the least time a start has, as a runner may download the
// agent first (a variable, which tests shorten).
var startLimit = 2 * time.Minute

// Name is the agent's name as its command line gives it (proc.Name), then its
// model: claude-agent-acp/default, codex-acp/gpt-6.1-sol, gemini/default.
func (a *Agent) Name() string { return proc.Name(a.Command) + "/" + cmp.Or(a.Model, "default") }

// SchemaMode is ai.SchemaPrompt: an agent takes no response format.
func (a *Agent) SchemaMode() string { return ai.SchemaPrompt }

// Window is the agent's context: Context, else the size the agent reported,
// or a smaller one it refused a request by (Learn); the room kept for an
// answer, ai.DefaultOutput, as an agent's output has no limit deepcode sets;
// the Overhead its first process showed; and the largest Scale of the last
// requests, at most 3.
func (a *Agent) Window() ai.Window {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := ai.Window{Context: cmp.Or(a.Context, a.size), Room: ai.DefaultOutput, Ratio: a.CompactRatio, Overhead: a.over, Scale: 1}
	if a.learned > 0 && (w.Context == 0 || a.learned < w.Context) {
		w.Context = a.learned
	}
	for _, s := range a.scales {
		w.Scale = max(w.Scale, min(s, 3))
	}
	return w
}

// Learn keeps n, a context an agent refused a longer request by.
func (a *Agent) Learn(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n > 0 {
		a.learned = n
	}
}

// observe keeps the Scale a request showed: in, the tokens the agent counted
// in it, less its Overhead, over local, the local tokenizer's count of what
// deepcode sent in it; for the last 20 requests of 1,000 tokens or more.
func (a *Agent) observe(in, local int) {
	if in <= 0 || local < 1000 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scales = append(a.scales, float64(in-a.over)/float64(local)); len(a.scales) > 20 {
		a.scales = a.scales[1:]
	}
}

// Close ends every process of the agent's, one starting too, and returns once
// they have ended; a request after it fails.
func (a *Agent) Close() {
	a.setup()
	a.mu.Lock()
	a.closed, a.now = true, nil
	var all []*running
	for r := range a.live {
		all = append(all, r)
	}
	a.mu.Unlock()
	var wg sync.WaitGroup
	for _, r := range all {
		wg.Go(func() { a.stop(r) })
	}
	wg.Wait()
	a.lock <- struct{}{} // a start under way ends, its process stopped, before Close does
	<-a.lock
}

// setup makes the turns and the lock, once.
func (a *Agent) setup() {
	a.once.Do(func() {
		a.slots, a.lock, a.live = make(chan struct{}, max(a.Sessions, 1)), make(chan struct{}, 1), map[*running]bool{}
	})
}

// agent is the running process, started when there is none, the last one
// ended, or it was retired, and one more session counted open on it, which
// release counts closed. A start that failed is tried again only after a
// backoff, the requests before it failing as it did: a shorter one when the
// agent refused, as the user may sign it in or fix the setting meanwhile.
func (a *Agent) agent(ctx context.Context) (*running, error) {
	a.setup()
	select {
	case a.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-a.lock }()
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.now
	switch {
	case a.closed:
		return nil, ai.Kinded(fmt.Errorf("%s: deepcode is shutting down", a.Name()), ai.ErrUnavailable)
	case r != nil && (r.retired || r.ended()):
		if r.ended() && !r.answered { // it started, then ended before it answered: a start that failed
			a.backoff(ai.Kinded(fmt.Errorf("%s: the agent ended before it answered (its log: %s)", a.Name(), filepath.Join(a.Dir, "agent.log")), ai.ErrUnavailable))
		}
		a.now, r.retired = nil, true
		if r.open == 0 {
			go a.stop(r)
		}
		r = nil
	}
	if r == nil {
		if time.Now().Before(a.retry) {
			return nil, a.last
		}
		a.mu.Unlock()
		var err error
		r, err = a.start()
		a.mu.Lock()
		if a.closed { // a start Close stopped is no failure of the agent's
			if err == nil {
				go a.stop(r)
			}
			return nil, ai.Kinded(fmt.Errorf("%s: deepcode is shutting down", a.Name()), ai.ErrUnavailable)
		}
		if err != nil {
			a.backoff(err)
			return nil, err
		}
		a.now = r // fails stay till it answers (note)
	}
	r.open++
	r.opened++
	r.retired = r.retired || r.opened >= recycle
	return r, nil
}

// backoff counts a start failed with err, under a.mu, and sets when the
// next may be tried: from 5 s, doubling, to 5 minutes, or at most 30 s when
// the agent refused, as the user may sign it in or fix the setting meanwhile.
func (a *Agent) backoff(err error) {
	a.fails++
	wait := min(5*time.Second<<min(a.fails-1, 6), 5*time.Minute)
	if errors.Is(err, ai.ErrRefused) {
		wait = min(wait, 30*time.Second)
	}
	a.last, a.retry, a.trouble, a.at = err, time.Now().Add(wait), err, time.Now()
	log.Printf("acp: %v", err)
}

// release counts a session of r closed, and ends r once a retired one has
// none open.
func (a *Agent) release(r *running) {
	a.mu.Lock()
	r.open--
	done := r.open == 0 && r.retired
	a.mu.Unlock()
	if done {
		go a.stop(r)
	}
}

// retire has r end once its sessions close, a new process taking its place.
func (a *Agent) retire(r *running) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r.retired = true
}

// ended reports whether r's process or connection has ended.
func (r *running) ended() bool {
	select {
	case <-r.conn.Done():
		return true
	case <-r.p.Done():
		return true
	default:
		return false
	}
}

// stop ends r's process tree, once, and forgets it.
func (a *Agent) stop(r *running) {
	r.p.Stop(grace)
	r.stderr.Close()
	a.mu.Lock()
	delete(a.live, r)
	a.mu.Unlock()
}

// start runs the agent's process, its standard error in Dir/agent.log,
// initializes it, and opens a session to check the model and effort it is
// set to; the first process also measures its Overhead and Scale (probe). It
// is bounded by Timeout, at least startLimit, the probe's requests each by
// Timeout, not by the request that needed it. Its log is appended to, and
// starts anew past 10 MB (rotate).
func (a *Agent) start() (*running, error) {
	limit := max(a.Timeout, startLimit)
	ctx, cancel := context.WithTimeoutCause(context.Background(), limit,
		ai.Kinded(fmt.Errorf("%s: not started within %s: %w", a.Name(), limit, context.DeadlineExceeded), ai.ErrUnavailable))
	defer cancel()
	dir, err := a.work(0) // the probe's folder, made before a process is: deepcode's disk, not the agent
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(a.Dir, "agent.log")
	rotate(logPath)
	stderr, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) // it may quote prompts; a retired process still writes to it
	if err != nil {
		return nil, ai.Kinded(fmt.Errorf("%s: %w", a.Name(), err), ai.ErrUnavailable)
	}
	p, err := proc.Start(a.Command, a.env(), stderr)
	if err != nil {
		stderr.Close()
		err = fmt.Errorf("%s: %w", a.Name(), err) // not the command line, which may hold a key
		if errors.Is(err, proc.ErrLine) {
			return nil, ai.Kinded(fmt.Errorf("%w (acp_command: give the agent's command, which no shell runs, every quote closed)", err), ai.ErrRefused)
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, ai.Kinded(fmt.Errorf("%w (acp_command: install the agent, or give its full path, as the OS user deepcode runs as)", err), ai.ErrRefused)
		}
		return nil, ai.Kinded(err, ai.ErrUnavailable)
	}
	r := &running{p: p, stderr: stderr, cl: &client{name: a.Name(), turns: map[acp.SessionId]*turn{}}}
	a.mu.Lock()
	a.live[r] = true
	closed := a.closed
	a.mu.Unlock()
	if closed { // after Close took the processes to stop
		a.stop(r)
		return nil, ai.Kinded(fmt.Errorf("%s: deepcode is shutting down", a.Name()), ai.ErrUnavailable)
	}
	r.conn = acp.NewClientSideConnection(r.cl, p.Stdin, p.Stdout)
	r.conn.SetLogger(slog.New(quiet{slog.NewTextHandler(log.Writer(), &slog.HandlerOptions{Level: slog.LevelWarn})})) // not each close
	info, err := r.conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{Meta: air}})
	if err != nil {
		a.stop(r)
		if ctx.Err() != nil {
			return nil, ai.Cause(ctx, err)
		}
		return nil, ai.Kinded(fmt.Errorf("%s: initialize: %w (its log: %s)", a.Name(), err, logPath), ai.ErrUnavailable)
	}
	r.kind, r.closes = kind(info, a.Command), info.AgentCapabilities.SessionCapabilities.Close != nil
	a.mu.Lock()
	a.info = info.AgentInfo
	a.mu.Unlock()
	a.warned.Do(func() { a.warn(r.kind) })
	if err := a.probe(ctx, r, dir); err != nil {
		a.stop(r)
		return nil, err
	}
	return r, nil
}

// rotate moves the log at path aside, to path.old, once it passes 10 MB; a
// process holding it open may keep it where it is (Windows).
func rotate(path string) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		_ = os.Rename(path, path+".old")
	}
}

// warn logs the settings an agent of kind leaves unused or unmet (design 5.6).
func (a *Agent) warn(kind string) {
	name := a.Name()
	if kind == "codex" && a.CodexHome == "" && !a.UserConfig {
		log.Printf("acp: %s: acp_codex_home is not set, so Codex reads your ~/.codex: its AGENTS.md, MCP servers, plugins and hooks "+
			"join every request; set it to a folder of its own and sign Codex in there (README, ACP)", name)
	}
	if kind != "codex" && a.CodexHome != "" {
		log.Printf("acp: %s is not Codex: acp_codex_home is not used", name)
	}
	if kind != "claude" && a.Connectors {
		log.Printf("acp: %s is not Claude: acp_claude_connectors is not used", name)
	}
	if kind == "" && !a.UserConfig {
		log.Printf("acp: %s is neither Claude nor Codex, whose options deepcode sets: the agent's own instructions, settings, "+
			"MCP servers and tools join every session (README, ACP)", name)
	}
}

// kind is which adapter the agent is, by the name it gave, else by its
// command line: claude, codex, or "".
func kind(info acp.InitializeResponse, line string) string {
	name := strings.ToLower(line)
	if info.AgentInfo != nil {
		name = strings.ToLower(info.AgentInfo.Name)
	}
	for _, k := range []string{"claude", "codex"} {
		if strings.Contains(name, k) {
			return k
		}
	}
	return ""
}

// probeAsk is the probe's request.
const probeAsk = "Reply with OK."

// sample is code to measure Scale by: some 3,000 tokens of Go.
var sample = func() string {
	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, "// handle%d routes a request to its handler and writes the answer.\nfunc handle%d(w http.ResponseWriter, r *http.Request) {\n"+
			"\tif r.Method != http.MethodGet {\n\t\thttp.Error(w, \"method not allowed\", %d)\n\t\treturn\n\t}\n\tfmt.Fprintf(w, \"step %%d\", len(r.URL.Path)+%d)\n}\n\n", i, i, 405+i, i)
	}
	return b.String()
}()

// probe opens a session on r in dir, which checks the options it is set to
// (open).
// The first process also asks it probeAsk twice, the second time after
// sample, each in the time a request has: the agent's count of the first
// request less the local count of what it sent (with the instructions the
// adapter took, bare for Claude's) is its Overhead, and what the second added
// over the local count of what it added, its Scale (design 5.1). An agent
// that counts nothing leaves them 0 and 1.
func (a *Agent) probe(ctx context.Context, r *running, dir string) error {
	a.mu.Lock()
	r.open, r.opened = r.open+1, r.opened+1 // as agent() counts a session: r is not yet a.now
	probed := a.probed || a.Count == nil
	a.mu.Unlock()
	s, err := a.open(ctx, r, dir, "")
	if err != nil {
		a.release(r)
		return ai.Cause(ctx, err) // the start's time ran out: why, an ai.ErrUnavailable
	}
	defer s.close()
	if probed {
		return nil
	}
	held := []string{s.static}
	ask := func(text string) (result, error) { // on the subscription too, so metered
		ctx, cancel := ai.WithTimeout(context.Background(), a.Timeout, a.Name())
		defer cancel()
		held = append(held, text)
		start := time.Now()
		res, err := s.ask(ctx, text, nil)
		a.meter(ctx, start, held, res, err)
		held = append(held, res.text)
		if err != nil && ctx.Err() != nil { // the agent did not answer in time: it may later
			err = ai.Kinded(ai.Cause(ctx, err), ai.ErrUnavailable)
		}
		return res, err
	}
	first, err := ask(probeAsk)
	if err != nil {
		return err
	}
	second, err := ask(sample + "\n\n" + probeAsk)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probed = true
	in1, in2 := counted(first.usage).InTokens, counted(second.usage).InTokens
	if in1 <= 0 || in2 <= in1 {
		log.Printf("acp: %s counts no tokens: its Overhead taken as 0 and its tokens as the local tokenizer's", a.Name())
		return nil
	}
	scale := float64(in2-in1) / float64(max(a.Count(first.text)+a.Count(sample+"\n\n"+probeAsk), 1))
	a.over = max(in1-int(scale*float64(a.Count(s.static)+a.Count(probeAsk))), 0)
	a.scales = append(a.scales, scale)
	if a.Context == 0 && a.size == 0 {
		log.Printf("acp: %s gives no context size: set chat_context", a.Name())
	}
	log.Printf("acp: %s: context %d tokens, Overhead %d, Scale %.2f", a.Name(), cmp.Or(a.Context, a.size), a.over, scale)
	return nil
}

// Status is what the admin console shows of an agent: the name and version it
// gave as it last started, the value of its model option, and the last
// failure since it last answered, a start's too, with its time.
type Status struct {
	Name, Version, Model string
	Trouble              string
	At                   time.Time
}

// Status is the agent's Status; empty but for the trouble before it starts.
func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Status{Model: a.model}
	if a.info != nil {
		s.Name, s.Version = a.info.Name, a.info.Version
	}
	if a.trouble != nil {
		s.Trouble, s.At = a.trouble.Error(), a.at
	}
	return s
}
