package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/acp/acptest"
	"deepcode/internal/platform/ai/usage"
)

// TestMain runs this test binary as the fake agent when asked to.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "acpfake" {
		acptest.Main(os.Args[2:])
	}
	os.Exit(m.Run())
}

// metered keeps what an Agent meters.
type metered struct {
	mu   sync.Mutex
	rows []usage.Usage
}

// Record keeps u.
func (m *metered) Record(_ context.Context, _ string, u usage.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u.Elapsed = 0 // the Windows clock may say 0 for a fast request
	m.rows = append(m.rows, u)
}

// all is what was metered so far.
func (m *metered) all() []usage.Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]usage.Usage(nil), m.rows...)
}

// quarter counts a fourth of a text's bytes as tokens, as the fake does.
func quarter(s string) int { return len(s) / 4 }

// fake is an Agent on this test binary run as the fake agent with flags, set
// as set says, closed when the test ends.
func fake(t *testing.T, flags string, set ...func(*Agent)) (*Agent, *metered) {
	m := &metered{}
	a := &Agent{Command: `"` + os.Args[0] + `" acpfake ` + flags, Dir: t.TempDir(), Timeout: time.Minute, Count: quarter, Meter: m}
	for _, f := range set {
		f(a)
	}
	t.Cleanup(a.Close)
	return a, m
}

// echo is what the fake got for p, fake:echo added to what is new.
func echo(t *testing.T, ctx context.Context, a *Agent, p ai.Prompt) map[string]any {
	t.Helper()
	p.Dynamic += " fake:echo"
	text, err := ai.Generate(ctx, a, p)
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(text), &got) != nil {
		t.Fatalf("echo: %q, %v", text, err)
	}
	return got
}

// at is the value at path in v, decoded JSON.
func at(v any, path ...string) any {
	for _, k := range path {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

// TestSessions: each request is a session in its repository's folder, its
// instructions where Claude's, Codex's or any other adapter takes them, the
// conversation as a transcript, the agent's environment without deepcode's keys,
// the user's settings and connectors off unless asked for, and Codex in its
// read-only mode.
func TestSessions(t *testing.T) {
	t.Setenv("DS_APIKEY", "secret")
	t.Setenv("DEEPCODE_CHAT_API_KEY", "secret")
	p := ai.Prompt{Static: "Be brief.", Previous: []ai.Message{{Role: "user", Content: "q1"}, {Role: "assistant", Content: "a1"}}, Dynamic: "q2"}
	claude, _ := fake(t, "-name claude-agent-acp", func(a *Agent) { a.CodexHome = "/codex" })
	got := echo(t, usage.WithRepository(context.Background(), 7), claude, p)
	if at(got, "static") != "Be brief." || filepath.Base(at(got, "cwd").(string)) != "7" || at(got, "air") != true ||
		!strings.HasPrefix(at(got, "prompt").(string), "<conversation>\n<user>\nq1\n</user>\n<assistant>\na1\n</assistant>\n</conversation>\n\nq2") {
		t.Fatalf("claude: %v", got)
	}
	options := at(got, "meta", "claudeCode", "options")
	if b, _ := json.Marshal(options); string(b) != `{"promptSuggestions":false,"settingSources":[],"settings":{"disableClaudeAiConnectors":true},"strictMcpConfig":true,"tools":[]}` {
		t.Errorf("claude's options: %s", b)
	}
	env := at(got, "env").(map[string]any)
	if env["DS_APIKEY"] != "" || env["DEEPCODE_CHAT_API_KEY"] != "" || env["PATH"] == "" || env["CODEX_HOME"] != "/codex" ||
		!strings.Contains(env["CODEX_CONFIG"].(string), `"skip_host_skill_discovery":true`) {
		t.Errorf("the agent's environment: %v", env)
	}
	if got := echo(t, context.Background(), claude, ai.Prompt{}); filepath.Base(at(got, "cwd").(string)) != "_" || !filepath.IsAbs(at(got, "cwd").(string)) ||
		at(got, "static") != bare {
		t.Errorf("a request of no repository and no instructions: %v", got)
	}

	mine, _ := fake(t, "-name claude-agent-acp", func(a *Agent) { a.UserConfig, a.Connectors = true, true })
	got = echo(t, context.Background(), mine, p)
	if b, _ := json.Marshal(at(got, "meta", "claudeCode", "options")); string(b) != `{"promptSuggestions":false,"settingSources":["user","project","local"],"settings":{"disableClaudeAiConnectors":false},"strictMcpConfig":false,"tools":[]}` ||
		at(got, "env", "CODEX_CONFIG") != "" || at(got, "env", "CODEX_HOME") != "" {
		t.Errorf("the user's settings and connectors asked for: %s, %v", b, at(got, "env"))
	}

	codex, _ := fake(t, "-name codex-acp -modes agent,read-only")
	got = echo(t, context.Background(), codex, p)
	if at(got, "static") != "Be brief." || at(got, "meta", "jetbrains", "air", "customInstructions") != "Be brief." || at(got, "config", "mode") != "read-only" {
		t.Errorf("codex: %v", got)
	}

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	other, _ := fake(t, "-name gemini-cli", func(a *Agent) { a.CodexHome, a.Connectors = "/codex", true })
	got = echo(t, context.Background(), other, p)
	if at(got, "meta") != nil || !strings.HasPrefix(at(got, "prompt").(string), "<instructions>\nBe brief.\n</instructions>\n\n<conversation>") {
		t.Errorf("another agent: %v", got)
	}
	if s := logged.String(); !strings.Contains(s, "acp_codex_home is not used") || !strings.Contains(s, "acp_claude_connectors is not used") || !strings.Contains(s, "neither Claude nor Codex") {
		t.Errorf("the settings another agent leaves unused: %q", s)
	}
}

// TestOptions: the model and effort set are chosen in every session, one of
// the values the agent lists, the efforts of the model chosen, or the agent
// does not start, saying which it lists; a session whose options cannot be
// set is counted closed once.
func TestOptions(t *testing.T) {
	pair, _ := fake(t, "-models opus,haiku -efforts high,max -efforts2 low,medium", func(a *Agent) { a.Model, a.Effort = "haiku", "low" })
	if got := echo(t, context.Background(), pair, ai.Prompt{}); at(got, "config", "model") != "haiku" || at(got, "config", "effort") != "low" {
		t.Errorf("an effort of the model chosen: %v", at(got, "config"))
	}
	unset, _ := fake(t, "-modes agent,read-only -setfail 9")
	for range 3 {
		if _, err := ai.Generate(usage.WithRepository(context.Background(), 9), unset, ai.Prompt{}); err == nil {
			t.Fatal("options that cannot be set")
		}
	}
	if unset.now.open != 0 { // a.now and r.open written by this goroutine alone
		t.Errorf("sessions counted open: %d", unset.now.open)
	}
	const flags = "-name codex-acp -models gpt-6,gpt-6-mini -efforts high,low"
	a, _ := fake(t, flags, func(a *Agent) { a.Model, a.Effort = "gpt-6-mini", "low" })
	if got := echo(t, context.Background(), a, ai.Prompt{}); at(got, "config", "model") != "gpt-6-mini" || at(got, "config", "effort") != "low" {
		t.Errorf("options: %v", at(got, "config"))
	}
	for name, set := range map[string]func(*Agent){
		"model":  func(a *Agent) { a.Model = "gpt-5" },
		"effort": func(a *Agent) { a.Effort = "ultra" },
	} {
		bad, _ := fake(t, flags, set)
		if _, err := ai.Generate(context.Background(), bad, ai.Prompt{}); !errors.Is(err, ai.ErrRefused) || !strings.Contains(err.Error(), "gpt-6, gpt-6-mini") &&
			!strings.Contains(err.Error(), "high, low") {
			t.Errorf("%s not one of the agent's: %v", name, err)
		}
	}
	none, _ := fake(t, "", func(a *Agent) { a.Model = "sonnet" })
	if _, err := ai.Generate(context.Background(), none, ai.Prompt{}); !errors.Is(err, ai.ErrRefused) || !strings.Contains(err.Error(), "no model option") {
		t.Errorf("a model for an agent with no model option: %v", err)
	}
}

// TestWindow: the first process measures the agent's Overhead and Scale, and
// the context comes from its usage updates unless set or learned smaller;
// requests show the Scale of what they send.
func TestWindow(t *testing.T) {
	a, m := fake(t, "-name claude-agent-acp -overhead 500 -scale 1.5 -size 200000") // its probe's instructions are bare
	if w := a.Window(); w.Context != 0 || w.Overhead != 0 || w.Scale != 1 || w.Room != ai.DefaultOutput {
		t.Fatalf("before the agent starts: %+v", w)
	}
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "hi"}); err != nil {
		t.Fatal(err)
	}
	w := a.Window()
	if w.Context != 200000 || math.Abs(float64(w.Overhead-500)) > 5 || math.Abs(w.Scale-1.5) > 0.02 {
		t.Fatalf("measured: %+v", w)
	}
	if rows := m.all(); len(rows) != 3 || rows[0].InTokens < 500 || rows[1].InTokens < rows[0].InTokens+1000 {
		t.Errorf("the probe's two requests and the one asked are each metered: %+v", rows)
	}
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: strings.Repeat("abcd", 2000)}); err != nil {
		t.Fatal(err)
	}
	a.Learn(150000)
	if w := a.Window(); w.Context != 150000 || math.Abs(w.Scale-1.5) > 0.02 {
		t.Errorf("learned, then a request of 2,000 tokens: %+v", w)
	}
	if a.observe(a.Window().Overhead+900, 300); math.Abs(a.Window().Scale-1.5) > 0.02 { // too small to tell
		t.Errorf("a request of 300 tokens moved the Scale to %v", a.Window().Scale)
	}
	if a.observe(500+10000, 1000); a.Window().Scale != 3 {
		t.Errorf("a Scale of 10 seen: %v, want at most 3", a.Window().Scale)
	}
	set, _ := fake(t, "-size 200000", func(a *Agent) { a.Context = 64000 })
	if _, err := ai.Generate(context.Background(), set, ai.Prompt{}); err != nil || set.Window().Context != 64000 {
		t.Errorf("chat_context over the agent's: %+v, %v", set.Window(), err)
	}
	silent, _ := fake(t, "-usage=false -size 200000")
	if _, err := ai.Generate(context.Background(), silent, ai.Prompt{}); err != nil {
		t.Fatal(err)
	}
	if w := silent.Window(); w.Overhead != 0 || w.Scale != 1 {
		t.Errorf("an agent that counts nothing: %+v", w)
	}
}

// TestMeter: a request is metered with the agent's count, its cache kept
// apart as OpenAI keeps it; one it counted nothing for, estimated when it was
// answered; a failed one, as a call.
func TestMeter(t *testing.T) {
	a, m := fake(t, "-name claude-agent-acp -overhead 100")
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{Static: "abcd", Dynamic: "efgh"}); err != nil {
		t.Fatal(err)
	}
	if rows := m.all(); len(rows) != 3 || rows[2] != (usage.Usage{InTokens: 100 + 2, InChars: 4 + 4, OutChars: 2}) { // after the probe's two
		t.Errorf("counted: %+v", rows)
	}
	silent, m := fake(t, "-name claude-agent-acp -usage=false")
	ai.Generate(context.Background(), silent, ai.Prompt{Static: "abcdabcd", Dynamic: "efgh"})
	ai.Generate(context.Background(), silent, ai.Prompt{Dynamic: "fake:refusal"})
	rows := m.all()
	if len(rows) != 4 || rows[2] != (usage.Usage{InChars: 8 + 4, OutChars: 2, EstIn: 2 + 1}) || rows[3] != (usage.Usage{InChars: len(bare) + 12}) {
		t.Errorf("not counted: %+v", rows)
	}
	other, m := fake(t, "-usage=false") // its instructions in the prompt, sent once
	ai.Generate(context.Background(), other, ai.Prompt{Static: "abcd", Dynamic: "efgh"})
	if sent := "<instructions>\nabcd\n</instructions>\n\nefgh"; len(m.all()) != 3 || m.all()[2] != (usage.Usage{InChars: len(sent), OutChars: 2, EstIn: len(sent) / 4}) {
		t.Errorf("another agent: %+v", m.all())
	}
}

// TestJSON: an answer asked for in JSON loses the one fence around it, and is
// read; one asked for in text keeps it.
func TestJSON(t *testing.T) {
	a, _ := fake(t, "")
	type obj struct {
		A int `json:"a"`
	}
	v, _, err := ai.StreamJSON(context.Background(), a, ai.Prompt{Dynamic: "fake:fence"}, ai.Schema[obj]("obj"), nil)
	if err != nil || v.A != 1 {
		t.Errorf("fenced JSON: %+v, %v", v, err)
	}
	if text, _ := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:fence"}); !strings.HasPrefix(text, "```json") {
		t.Errorf("a text answer: %q", text)
	}
	for in, want := range map[string]string{"```json\n{}\n```": "{}", " ```\n[1]\n``` ": "[1]", "```{}```": "```{}```", "{}": "{}",
		"```go\nx\n```\n```\ny\n```": "```go\nx\n```\n```\ny\n```", "Here:\n```json\n{}\n```": "Here:\n```json\n{}\n```"} {
		if got := unfence(in); got != want {
			t.Errorf("unfence(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFailures: each failure design 5.5 lists is the error it says, and
// tells the user what to do where there is something to do.
func TestFailures(t *testing.T) {
	a, _ := fake(t, "-name claude-agent-acp")
	for what, want := range map[string]struct {
		kind  error
		words string
	}{
		"fake:login": {ai.ErrRefused, "claude /login"}, "fake:failure:access:login": {ai.ErrRefused, "codex login"},
		"fake:failure:limit:": {ai.ErrUnavailable, "It failed: resets at 17:00"}, "fake:failure:limit:retry": {ai.ErrUnavailable, ""},
		"fake:failure:service:retry": {ai.ErrUnavailable, ""}, "fake:failure:connection:new_session": {ai.ErrUnavailable, ""},
		"fake:failure:limit:new_session": {nil, "It failed"}, "fake:failure:request:": {ai.ErrFailed, ""},
		"fake:refusal": {ai.ErrFailed, "refusal"}, "fake:big": {nil, "passed 1 MB"},
	} {
		_, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: what})
		if err == nil || want.kind != nil && !errors.Is(err, want.kind) || !strings.Contains(err.Error(), want.words) ||
			want.kind == nil && strings.Contains(want.words, "It failed") && !errors.As(err, new(*ai.ErrContext)) {
			t.Errorf("%s: %v", what, err)
		}
	}
	if text, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:warning"}); err != nil || text != "OK" {
		t.Errorf("a warning: %q, %v", text, err)
	}
	if r, err := a.Ask(context.Background(), ai.Prompt{Dynamic: "fake:spent"}, nil, nil); err != nil || !r.Spent || r.Text != `{"a": ` {
		t.Errorf("an answer cut at the budget: %+v, %v", r, err)
	}
	if text, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:tool"}); err != nil || text != "permission no; read refused: true" {
		t.Errorf("the agent's own tool: %q, %v", text, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := ai.Generate(ctx, a, ai.Prompt{Dynamic: "fake:slow"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a request past its time: %v", err)
	}
	for _, line := range []string{"no-such-agent-x7 --acp", `"` + os.Args[0] + `" acpfake -login`} {
		gone, _ := fake(t, "", func(a *Agent) { a.Command = line })
		for range 2 { // the second within the backoff, failing as the first did
			if _, err := ai.Generate(context.Background(), gone, ai.Prompt{}); !errors.Is(err, ai.ErrRefused) {
				t.Errorf("%s: %v", line, err)
			}
		}
		if gone.fails != 1 {
			t.Errorf("%s: started %d times within the backoff", line, gone.fails)
		}
		gone.retry, gone.fails = time.Time{}, 6
		ai.Generate(context.Background(), gone, ai.Prompt{})
		if wait := time.Until(gone.retry); wait > 30*time.Second || wait < 20*time.Second {
			t.Errorf("%s: tried again in %v after 7 refused starts, want 30s", line, wait)
		}
	}
}

// TestClose: Close ends every process of the agent's, a retired one still
// serving a session too, before it returns.
func TestClose(t *testing.T) {
	a, _ := fake(t, "", func(a *Agent) { a.Sessions = 2 })
	ai.Generate(context.Background(), a, ai.Prompt{}) // starts the process
	done := make(chan error)
	go func() { _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:slow"}); done <- err }()
	time.Sleep(200 * time.Millisecond)
	a.retire(a.now)
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{}); err != nil { // on a new process
		t.Fatal(err)
	}
	a.Close()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, context.DeadlineExceeded) { // ended, or cancelled by the agent as it ends
			t.Errorf("the retired process's session: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close left the retired process running")
	}
	if a.mu.Lock(); len(a.live) != 0 {
		t.Errorf("processes left: %d", len(a.live))
	}
	a.mu.Unlock()
}

// TestRestart: a process that ends, or whose thread fails silently, is
// replaced for the next request, as is one that opened recycle sessions.
func TestRestart(t *testing.T) {
	a, _ := fake(t, "-name codex-acp")
	pid := func() any { return at(echo(t, context.Background(), a, ai.Prompt{}), "pid") }
	first := pid()
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:crash"}); !errors.Is(err, ai.ErrUnavailable) || !strings.Contains(err.Error(), "agent.log") {
		t.Errorf("an agent that ended: %v", err)
	}
	second := pid()
	if second == first {
		t.Error("the agent that ended was not replaced")
	}
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:systemerror"}); !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("a silent system error: %v", err)
	}
	third := pid()
	if third == second {
		t.Error("the agent whose thread failed was not replaced")
	}
	for range recycle - 2 { // with the probe's session and the pid's, recycle on this process
		if _, err := ai.Generate(context.Background(), a, ai.Prompt{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := echo(t, context.Background(), a, ai.Prompt{}); at(got, "pid") == third || at(got, "opened") != 2.0 {
		t.Errorf("after %d sessions: %v", recycle, got)
	}
	a.Close()
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{}); !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("after Close: %v", err)
	}
}

// TestTurns: no more prompts run at a time than Sessions; a request waiting
// for its turn gives up with its context.
func TestTurns(t *testing.T) {
	a, _ := fake(t, "", func(a *Agent) { a.Sessions = 1 })
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{}); err != nil { // started
		t.Fatal(err)
	}
	slow, stop := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { _, err := ai.Generate(slow, a, ai.Prompt{Dynamic: "fake:slow"}); done <- err }()
	time.Sleep(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := ai.Generate(ctx, a, ai.Prompt{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a second prompt with one turn: %v", err)
	}
	stop()
	<-done
	if text, err := ai.Generate(context.Background(), a, ai.Prompt{}); err != nil || text != "OK" {
		t.Errorf("once the turn is back: %q, %v", text, err)
	}
}

// TestName: an agent's name is its package's or program's, then its model.
func TestName(t *testing.T) {
	for line, want := range map[string]string{
		"npx -y @agentclientprotocol/claude-agent-acp@0.107.3": "claude-agent-acp/default", "gemini --acp": "gemini/default",
		`"C:\Program Files\nodejs\npx.cmd" -y @agentclientprotocol/codex-acp`: "codex-acp/default", "pnpm dlx opencode-ai acp": "opencode-ai/default",
		`C:\bin\opencode.exe acp`: "opencode/default", "": "acp/default",
	} {
		if got := (&Agent{Command: line}).Name(); got != want {
			t.Errorf("%q: %s, want %s", line, got, want)
		}
	}
	if got := (&Agent{Command: "gemini --acp", Model: "gemini-3-pro"}).Name(); got != "gemini/gemini-3-pro" {
		t.Errorf("with a model: %s", got)
	}
}

// TestExchange: a gathering is one session, its instructions ending with the
// tools and the reply's format; each reply's calls are read by their schema,
// the results go back with the next request, a reply that is not that JSON
// is asked for again once, and Close closes the session.
func TestExchange(t *testing.T) {
	type args struct {
		Query string `json:"query"`
	}
	tools := []ai.Tool{ai.Schema[args]("search").Tool("Search the code"), ai.Schema[struct{}]("done").Tool("Say you are done")}
	a, m := fake(t, "-name claude-agent-acp")
	ctx := context.Background()
	script := `fake:script:{"calls": [{"tool": "search", "arguments": {"query": "router"}}, {"tool": "done", "arguments": {}}]}|||not JSON|||{"calls": []}`
	ex := a.Exchange(ai.Prompt{Static: "Gather.", Dynamic: script}, tools)
	if next, _ := ex.Size(quarter); next < 100 {
		t.Errorf("the first request's size: %d", next)
	}
	_, calls, err := ex.Next(ctx)
	if err != nil || len(calls) != 2 || calls[0].Name != "search" || calls[0].Arguments != `{"query": "router"}` || calls[0].Key != 0 || calls[1].Key != 1 {
		t.Fatalf("the first reply: %+v, %v", calls, err)
	}
	e := ex.(*exchange)
	if static := e.Piece(0); !strings.HasPrefix(static, "Gather.\n\n"+callsLine+"\n- search: Search the code\n- done: Say you are done\n\n"+ai.SchemaLine) {
		t.Errorf("the instructions: %q", static)
	}
	ex.Answer(calls[0], "main.go:1-9")
	ex.Answer(calls[1], "Done.")
	if ex.Added() != 3 || ex.Used().InTokens == 0 {
		t.Errorf("after the first reply: %d added, used %+v", ex.Added(), ex.Used())
	}
	if _, calls, err = ex.Next(ctx); err != nil || len(calls) != 0 {
		t.Fatalf("the reply asked for again: %+v, %v", calls, err)
	}
	texts := e.Texts(e.Len())
	if len(texts) != 8 || texts[3] != "<result call=\"0\" tool=\"search\">\nmain.go:1-9\n</result>" || texts[5] != "not JSON" ||
		!strings.HasPrefix(texts[6], "Your reply is not the JSON asked for") || ex.Compact(map[int]string{0: "x"}) != 0 {
		t.Errorf("the exchange: %q", texts)
	}
	held := len("\n\n") // between the two results, sent as one prompt
	for _, s := range texts[:7] {
		held += len(s)
	}
	if ex.Used().InTokens != held/4 { // the fake counts all a session holds: the results were sent
		t.Errorf("the request asked again counted %d tokens, want %d", ex.Used().InTokens, held/4)
	}
	if rows := m.all(); len(rows) != 2+3 {
		t.Errorf("metered: %+v", rows)
	}
	ex.Close()
	if _, _, err := ex.Next(ctx); err == nil {
		t.Error("Next after Close")
	}
	if got := echo(t, ctx, a, ai.Prompt{}); at(got, "open") != 1.0 {
		t.Errorf("the exchange's session is still open: %v", got)
	}
	for what, want := range map[string]error{"fake:script:no|||still no": ai.ErrFailed, "fake:spent": ai.ErrSpent} {
		ex := a.Exchange(ai.Prompt{Dynamic: what}, tools)
		if _, _, err := ex.Next(ctx); !errors.Is(err, want) {
			t.Errorf("%s: %v", what, err)
		}
		ex.Close()
	}
	ex = a.Exchange(ai.Prompt{Dynamic: `fake:script:not JSON|||fake:slow|||{"calls": []}`}, tools)
	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, _, err = ex.Next(short)
	cancel()
	e = ex.(*exchange)
	texts = e.Texts(e.Len())
	held = len(texts[len(texts)-1]) // the request asked again, sent again
	for _, s := range texts {
		held += len(s)
	}
	if _, calls, again := ex.Next(ctx); !errors.Is(err, context.DeadlineExceeded) || again != nil || len(calls) != 0 || ex.Used().InTokens != held/4 {
		t.Errorf("after a request that failed: %v, then %+v, %v, %d tokens, want %d", err, calls, again, ex.Used().InTokens, held/4)
	}
	ex.Close()
}
