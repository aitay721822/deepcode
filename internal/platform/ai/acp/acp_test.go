package acp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

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

// fake is an Agent on this test binary run as the fake agent with flags, set
// as set says, closed when the test ends.
func fake(t *testing.T, flags string, set ...func(*Agent)) (*Agent, *acptest.Metered) {
	m := &acptest.Metered{}
	a := &Agent{Command: `"` + os.Args[0] + `" acpfake ` + flags, Dir: t.TempDir(), Timeout: time.Minute, Count: acptest.Quarter, Meter: m}
	for _, f := range set {
		f(a)
	}
	t.Cleanup(a.Close)
	return a, m
}

// TestOptions: the model and effort set are chosen in every session, one of
// the values the agent lists, the efforts of the model chosen, or the agent
// does not start, saying which it lists; a session whose options cannot be
// set is counted closed once.
func TestOptions(t *testing.T) {
	pair, _ := fake(t, "-models opus,haiku -efforts high,max -efforts2 low,medium", func(a *Agent) { a.Model, a.Effort = "haiku", "low" })
	if got := acptest.Echo(t, context.Background(), pair, ai.Prompt{}); acptest.At(got, "config", "model") != "haiku" || acptest.At(got, "config", "effort") != "low" {
		t.Errorf("an effort of the model chosen: %v", acptest.At(got, "config"))
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
	if got := acptest.Echo(t, context.Background(), a, ai.Prompt{}); acptest.At(got, "config", "model") != "gpt-6-mini" || acptest.At(got, "config", "effort") != "low" {
		t.Errorf("options: %v", acptest.At(got, "config"))
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
	_, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "hi"})
	if w := a.Window(); err != nil || w.Context != 200000 || math.Abs(float64(w.Overhead-500)) > 5 || math.Abs(w.Scale-1.5) > 0.02 {
		t.Fatalf("measured: %+v, %v", w, err)
	}
	if rows := m.All(); len(rows) != 3 || rows[0].InTokens < 500 || rows[1].InTokens < rows[0].InTokens+1000 {
		t.Errorf("the probe's two requests and the one asked are each metered: %+v", rows)
	}
	_, err = ai.Generate(context.Background(), a, ai.Prompt{Dynamic: strings.Repeat("abcd", 2000)})
	if a.Learn(150000); err != nil || a.Window().Context != 150000 || math.Abs(a.Window().Scale-1.5) > 0.02 {
		t.Errorf("learned, then a request of 2,000 tokens: %+v, %v", a.Window(), err)
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
	if _, err := ai.Generate(context.Background(), silent, ai.Prompt{}); err != nil || silent.Window().Overhead != 0 || silent.Window().Scale != 1 {
		t.Errorf("an agent that counts nothing: %+v, %v", silent.Window(), err)
	}
}

// TestMeter: a request is metered with the agent's count, its cache kept
// apart as OpenAI keeps it; one it counted nothing for, estimated when it was
// answered; a failed one, as a call.
func TestMeter(t *testing.T) {
	a, m := fake(t, "-name claude-agent-acp -overhead 100")
	_, err := ai.Generate(context.Background(), a, ai.Prompt{Static: "abcd", Dynamic: "efgh"})
	if rows := m.All(); err != nil || len(rows) != 3 || rows[2] != (usage.Usage{InTokens: 100 + 2, InChars: 4 + 4, OutChars: 2}) { // after the probe's two
		t.Errorf("counted: %+v, %v", rows, err)
	}
	silent, m := fake(t, "-name claude-agent-acp -usage=false")
	ai.Generate(context.Background(), silent, ai.Prompt{Static: "abcdabcd", Dynamic: "efgh"})
	ai.Generate(context.Background(), silent, ai.Prompt{Dynamic: "fake:refusal"})
	rows := m.All()
	if len(rows) != 4 || rows[2] != (usage.Usage{InChars: 8 + 4, OutChars: 2, EstIn: 2 + 1}) || rows[3] != (usage.Usage{InChars: len(bare) + 12}) {
		t.Errorf("not counted: %+v", rows)
	}
	other, m := fake(t, "-usage=false") // its instructions in the prompt, sent once
	ai.Generate(context.Background(), other, ai.Prompt{Static: "abcd", Dynamic: "efgh"})
	if sent := "<instructions>\nabcd\n</instructions>\n\nefgh"; len(m.All()) != 3 || m.All()[2] != (usage.Usage{InChars: len(sent), OutChars: 2, EstIn: len(sent) / 4}) {
		t.Errorf("another agent: %+v", m.All())
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
	for what, want := range map[string]string{"fake:warning": "OK", "fake:tool": "permission no; read refused: true"} { // the agent's own tool
		if text, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: what}); err != nil || text != want {
			t.Errorf("%s: %q, %v", what, text, err)
		}
	}
	if r, err := a.Ask(context.Background(), ai.Prompt{Dynamic: "fake:spent"}, nil, nil); err != nil || !r.Spent || r.Text != `{"a": ` {
		t.Errorf("an answer cut at the budget: %+v, %v", r, err)
	}
	for _, line := range []string{"no-such-agent-x7 --acp", `"` + os.Args[0] + `" acpfake -login`, `npx "unclosed`} {
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
	defer func(was time.Duration) { startLimit = was }(startLimit)
	startLimit = 300 * time.Millisecond
	for _, flag := range []string{"-hang", "-stall session/new", "-stall initialize"} { // the probe's request, its session/new, initialize, past its time
		hung, _ := fake(t, flag, func(a *Agent) { a.Timeout = 300 * time.Millisecond })
		ai.Generate(context.Background(), hung, ai.Prompt{})
		if !errors.Is(hung.last, ai.ErrUnavailable) || !errors.Is(hung.last, context.DeadlineExceeded) || !strings.Contains(hung.last.Error(), "within") {
			t.Errorf("%s: %v", flag, hung.last)
		}
	}
}

// TestStatus: an agent's Status is what it said it is, its model option's
// value, and its last failure till it answers again; a process that ends
// before it answered is a start that failed, tried again after the backoff.
func TestStatus(t *testing.T) {
	ctx := context.Background()
	a, _ := fake(t, "-name claude-agent-acp -models opus,haiku")
	if s := a.Status(); s != (Status{}) {
		t.Errorf("before it starts: %+v", s)
	}
	ai.Generate(ctx, a, ai.Prompt{Dynamic: "fake:failure:limit:"})
	if s := a.Status(); s.Name != "claude-agent-acp" || s.Version != "1.0.0" || s.Model != "opus" || !strings.Contains(s.Trouble, "It failed") || s.At.IsZero() {
		t.Errorf("after a failure: %+v", s)
	}
	if _, err := ai.Generate(ctx, a, ai.Prompt{}); err != nil || a.Status().Trouble != "" {
		t.Errorf("after an answer: %+v, %v", a.Status(), err)
	}
	for range 2 { // the first process answered the probe, the second nothing
		ai.Generate(ctx, a, ai.Prompt{Dynamic: "fake:crash"})
	}
	if _, err := ai.Generate(ctx, a, ai.Prompt{}); !errors.Is(err, ai.ErrUnavailable) || !strings.Contains(err.Error(), "ended before it answered") || a.fails != 1 {
		t.Errorf("after a process that never answered: %v, %d starts failed", err, a.fails)
	}
	short, _ := fake(t, "-quota 5000") // the probe's first request is answered, its second, with the sample, not
	ai.Generate(ctx, short, ai.Prompt{})
	short.retry = time.Time{}
	ai.Generate(ctx, short, ai.Prompt{})
	if short.fails != 2 || short.Status().Trouble == "" {
		t.Errorf("a probe failed twice on its second request: %d starts failed, %+v", short.fails, short.Status())
	}
	slow, _ := fake(t, "", func(a *Agent) { a.Timeout = time.Second })
	gone, cancel := context.WithCancel(ctx)
	time.AfterFunc(200*time.Millisecond, cancel)
	ai.Generate(gone, slow, ai.Prompt{Dynamic: "fake:slow"}) // a reader who left: not the agent's failure
	left := slow.Status().Trouble
	due, cancelDue := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelDue()
	ai.Generate(due, slow, ai.Prompt{Dynamic: "fake:slow"}) // a job's own deadline: nor that
	left += slow.Status().Trouble
	if _, err := ai.Generate(ctx, slow, ai.Prompt{Dynamic: "fake:slow"}); left != "" || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(slow.Status().Trouble, "no whole answer") {
		t.Errorf("a request its caller ended: %q; one past its time: %v, %q", left, err, slow.Status().Trouble)
	}
	ai.Generate(ctx, slow, ai.Prompt{Dynamic: "fake:logout"}) // answered, then signed out: each session/new refused
	if _, err := ai.Generate(ctx, slow, ai.Prompt{}); !errors.Is(err, ai.ErrRefused) || !strings.Contains(slow.Status().Trouble, "not signed in") {
		t.Errorf("signed out once it answered: %v, %+v", err, slow.Status())
	}
	blocked, _ := fake(t, "", func(b *Agent) { b.Dir = filepath.Join(slow.Dir, "agent.log", "acp") }) // under a file: deepcode's failure, not the agent's
	if _, err := ai.Generate(ctx, blocked, ai.Prompt{}); !errors.Is(err, ai.ErrUnavailable) || blocked.fails != 0 || blocked.Status().Trouble != "" {
		t.Errorf("no folder to work in: %v, %d starts failed, %+v", err, blocked.fails, blocked.Status())
	}
}

// TestLog: the agent's log is appended to, and set aside past 10 MB.
func TestLog(t *testing.T) {
	a, _ := fake(t, "")
	file := filepath.Join(a.Dir, "agent.log")
	if err := os.WriteFile(file, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ai.Generate(context.Background(), a, ai.Prompt{})
	a.Close()
	if b, err := os.ReadFile(file); err != nil || !bytes.HasPrefix(b, []byte("before\n")) {
		t.Errorf("the log of the start before: %q, %v", b, err)
	}
	if err := os.WriteFile(file, make([]byte, 10<<20+1), 0o600); err != nil {
		t.Fatal(err)
	}
	again, _ := fake(t, "", func(b *Agent) { b.Dir = a.Dir })
	ai.Generate(context.Background(), again, ai.Prompt{})
	if fi, err := os.Stat(file + ".old"); err != nil || fi.Size() != 10<<20+1 {
		t.Errorf("a log past 10 MB was not set aside: %v", err)
	}
}

// TestClose: Close ends every process of the agent's, a retired one still
// serving a session too, before it returns; a start it stopped is no failure.
func TestClose(t *testing.T) {
	stalled, _ := fake(t, "-stall session/new")
	go ai.Generate(context.Background(), stalled, ai.Prompt{})
	acptest.Await(t, stalled.Dir, "session/new") // its start's probe; Close takes the lock the start holds, so sees its end
	if stalled.Close(); stalled.fails != 0 || stalled.trouble != nil {
		t.Errorf("a start Close stopped: %d starts failed, %v", stalled.fails, stalled.trouble)
	}
	a, _ := fake(t, "", func(a *Agent) { a.Sessions = 2 })
	ai.Generate(context.Background(), a, ai.Prompt{}) // starts the process
	done := make(chan error)
	go func() { _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:slow"}); done <- err }()
	acptest.Await(t, a.Dir, "a slow prompt")
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
	pid := func() any { return acptest.At(acptest.Echo(t, context.Background(), a, ai.Prompt{}), "pid") }
	first := pid()
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:crash"}); !errors.Is(err, ai.ErrUnavailable) || !strings.Contains(err.Error(), "agent.log") {
		t.Errorf("an agent that ended: %v", err)
	}
	second := pid()
	if second == first {
		t.Error("the agent that ended was not replaced")
	}
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{Dynamic: "fake:systemerror"}); !errors.Is(err, ai.ErrUnavailable) || !strings.Contains(err.Error(), "usage limit") {
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
	if got := acptest.Echo(t, context.Background(), a, ai.Prompt{}); acptest.At(got, "pid") == third || acptest.At(got, "opened") != 2.0 {
		t.Errorf("after %d sessions: %v", recycle, got)
	}
	a.Close()
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{}); !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("after Close: %v", err)
	}
}

// TestName: an agent's name is its command line's (proc.Name), then its
// model.
func TestName(t *testing.T) {
	if none, set := (&Agent{Command: "gemini --acp"}).Name(), (&Agent{Command: "gemini --acp", Model: "gemini-3-pro"}).Name(); none != "gemini/default" || set != "gemini/gemini-3-pro" {
		t.Errorf("without a model: %s; with one: %s", none, set)
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
	if next, _ := ex.Size(acptest.Quarter); next < 100 {
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
	if rows := m.All(); len(rows) != 2+3 {
		t.Errorf("metered: %+v", rows)
	}
	ex.Close()
	if _, _, err := ex.Next(ctx); err == nil {
		t.Error("Next after Close")
	}
	if got := acptest.Echo(t, ctx, a, ai.Prompt{}); acptest.At(got, "open") != 1.0 {
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
	defer func(was time.Duration) { grace = was }(grace)
	grace = 300 * time.Millisecond
	ex = a.Exchange(ai.Prompt{Dynamic: "fake:deaf"}, tools) // its turn never ends
	short, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
	_, _, err = ex.Next(short)
	cancel()
	if _, _, again := ex.Next(ctx); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(again, ai.ErrUnavailable) || !strings.Contains(again.Error(), "did not end") {
		t.Errorf("a turn not ended once cancelled: %v, then %v", err, again)
	}
	ex.Close()
}

// TestQuiet: a notification the SDK has no method for is dropped from the
// connection's log, as a client drops it; any other failure is logged.
func TestQuiet(t *testing.T) {
	var b bytes.Buffer
	l := slog.New(quiet{slog.NewTextHandler(&b, nil)}).With("conn", 1).WithGroup("rpc") // a logger the SDK may derive
	l.Error("failed to handle notification", "method", "auth/status_update", "err", acp.NewMethodNotFound("auth/status_update"))
	l.Error("failed to handle notification", "method", "session/update", "err", acp.NewInvalidParams(nil))
	if s := b.String(); strings.Contains(s, "auth/status_update") || !strings.Contains(s, "session/update") {
		t.Errorf("logged: %q", s)
	}
}
