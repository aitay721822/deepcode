// Package acptest is a fake ACP agent for tests: a test binary runs itself as
// one, its TestMain handing Main the arguments after "acpfake". Its sessions
// answer as their prompts say (fake:<what>, below), or ask an
// OpenAI-compatible server (-relay), and count tokens as a fourth of the
// bytes a session holds, times -scale, plus -overhead.
package acptest

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/compat"
	"deepcode/internal/platform/ai/usage"
)

// fake is the agent: its flags and its sessions.
type fake struct {
	conn                             *acp.AgentSideConnection
	name, relay, setfail, stall      string
	overhead, size, quota            int
	scale                            float64
	counts, login, hang              bool
	models, efforts, efforts2, modes []string
	mu                               sync.Mutex
	air                              bool // the client declared the AIR extension
	sessions                         map[acp.SessionId]*session
	opened, closed                   int
}

// session is what a session holds: its folder, its options, its
// instructions, the bytes of all it was sent and answered, its options' values,
// the replies its prompts have left (fake:script), and whether a turn runs.
type session struct {
	cwd     string
	meta    map[string]any
	static  string
	bytes   int
	config  map[string]string
	replies []string
	busy    bool
}

// Main serves one connection on standard input and output, then exits. Its
// flags: -name, the name the agent gives; -overhead and -scale, its count;
// -usage=false, a count it keeps to itself; -size, the context its usage
// updates report (none when 0); -models, -efforts and -modes, its options'
// values, comma separated, the first one current, and -efforts2 the efforts
// once the model is not the first; -setfail, the folder whose sessions' options
// cannot be set; -login, a session/new that fails as not signed in; -stall
// initialize or session/new, that request answered by nothing; -hang, prompts
// answered by nothing till cancelled; -quota, the bytes past which a prompt
// fails as over the subscription's limit; -relay, the base URL of a server
// each prompt asks.
func Main(args []string) {
	f := &fake{sessions: map[acp.SessionId]*session{}}
	flags := flag.NewFlagSet("acpfake", flag.ExitOnError)
	flags.StringVar(&f.name, "name", "fake-agent", "")
	flags.IntVar(&f.overhead, "overhead", 0, "")
	flags.Float64Var(&f.scale, "scale", 1, "")
	flags.BoolVar(&f.counts, "usage", true, "")
	flags.IntVar(&f.size, "size", 0, "")
	models, efforts, modes := flags.String("models", "", ""), flags.String("efforts", "", ""), flags.String("modes", "", "")
	efforts2 := flags.String("efforts2", "", "")
	flags.StringVar(&f.setfail, "setfail", "", "")
	flags.BoolVar(&f.login, "login", false, "")
	flags.BoolVar(&f.hang, "hang", false, "")
	flags.StringVar(&f.stall, "stall", "", "")
	flags.IntVar(&f.quota, "quota", 0, "")
	flags.StringVar(&f.relay, "relay", "", "")
	flags.Parse(args)
	for _, o := range []struct {
		to   *[]string
		from string
	}{{&f.models, *models}, {&f.efforts, *efforts}, {&f.efforts2, *efforts2}, {&f.modes, *modes}} {
		if o.from != "" {
			*o.to = strings.Split(o.from, ",")
		}
	}
	f.conn = acp.NewAgentSideConnection(f, os.Stdout, os.Stdin)
	<-f.conn.Done()
	os.Exit(0)
}

// Initialize names the agent and offers session/close.
func (f *fake) Initialize(ctx context.Context, q acp.InitializeRequest) (acp.InitializeResponse, error) {
	if f.stall == "initialize" {
		<-ctx.Done()
		return acp.InitializeResponse{}, ctx.Err()
	}
	f.mu.Lock()
	_, f.air = q.ClientCapabilities.Meta["jetbrains"]
	f.mu.Unlock()
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber, AgentInfo: &acp.Implementation{Name: f.name, Version: "1.0.0"},
		AgentCapabilities: acp.AgentCapabilities{SessionCapabilities: acp.SessionCapabilities{Close: &acp.SessionCloseCapabilities{}}}}, nil
}

// preset stands for Claude Code's own system prompt, which Claude's adapter
// gives a session whose system prompt is empty.
const preset = "(Claude Code's system prompt)"

// NewSession opens a session with its options, its instructions taken from
// its _meta as Claude's and Codex's adapters take them.
func (f *fake) NewSession(ctx context.Context, q acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if f.stall == "session/new" {
		waits("session/new")
		<-ctx.Done()
		return acp.NewSessionResponse{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.login {
		return acp.NewSessionResponse{}, acp.NewAuthRequired(nil)
	}
	f.opened++
	id := acp.SessionId(fmt.Sprint("s", f.opened))
	s := &session{cwd: q.Cwd, meta: q.Meta, config: map[string]string{}}
	if p, ok := q.Meta["systemPrompt"].(string); ok {
		s.static = cmp.Or(p, preset)
	} else if jb, ok := q.Meta["jetbrains"].(map[string]any); ok {
		s.static, _ = jb["air"].(map[string]any)["customInstructions"].(string)
	}
	s.bytes = len(s.static)
	f.sessions[id] = s
	return acp.NewSessionResponse{SessionId: id, ConfigOptions: f.options(s)}, nil
}

// options are s's options as its values stand, a value no longer offered
// back to the first: the efforts are -efforts2's once the model is not the
// first.
func (f *fake) options(s *session) []acp.SessionConfigOption {
	efforts := f.efforts
	if len(f.efforts2) > 0 && slices.Index(f.models, s.config["model"]) > 0 {
		efforts = f.efforts2
	}
	var options []acp.SessionConfigOption
	for _, o := range []struct {
		id     string
		cat    acp.SessionConfigOptionCategory
		values []string
	}{{"model", acp.SessionConfigOptionCategoryModel, f.models}, {"effort", acp.SessionConfigOptionCategoryThoughtLevel, efforts},
		{"mode", acp.SessionConfigOptionCategoryMode, f.modes}} {
		if len(o.values) == 0 {
			continue
		}
		var list acp.SessionConfigSelectOptionsUngrouped
		for _, v := range o.values {
			list = append(list, acp.SessionConfigSelectOption{Name: v, Value: acp.SessionConfigValueId(v)})
		}
		if !slices.Contains(o.values, s.config[o.id]) {
			s.config[o.id] = o.values[0]
		}
		options = append(options, acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{Id: acp.SessionConfigId(o.id), Name: o.id,
			Category: &o.cat, CurrentValue: acp.SessionConfigValueId(s.config[o.id]), Options: acp.SessionConfigSelectOptions{Ungrouped: &list}, Type: "select"}})
	}
	return options
}

// SetSessionConfigOption keeps an option's value and returns the options as
// they now stand, failing in the -setfail folder.
func (f *fake) SetSessionConfigOption(_ context.Context, q acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[q.ValueId.SessionId]
	if s == nil || filepath.Base(s.cwd) == f.setfail {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams("the option cannot be set")
	}
	s.config[string(q.ValueId.ConfigId)] = string(q.ValueId.Value)
	return acp.SetSessionConfigOptionResponse{ConfigOptions: f.options(s)}, nil
}

// CloseSession counts a session closed.
func (f *fake) CloseSession(_ context.Context, q acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, q.SessionId)
	f.closed++
	return acp.CloseSessionResponse{}, nil
}

// Prompt answers as the prompt says, the first marker found:
//
//	fake:echo        what the session and the process got, as JSON
//	fake:script:A|||B  A now, B to the session's next prompt, and so on
//	fake:fence       a JSON object in a ```json fence
//	fake:spent       a partial answer stopped at the output budget
//	fake:refusal     a stop for a refusal
//	fake:login       a JSON-RPC error: not signed in
//	fake:failure:C:A an AIR session failure of category C and action A
//	fake:warning     an AIR warning, then OK
//	fake:systemerror Codex's silent system error: no answer
//	fake:crash       the process exits
//	fake:logout      OK, then every session/new fails as not signed in
//	fake:tool        a tool call, a permission and a file asked for
//	fake:slow        nothing till the prompt is cancelled, its turn ended 100 ms later
//	fake:deaf        nothing, a cancel too
//	fake:big         an answer of 1.1 MB
//
// else the session's next scripted reply (fake:slow among them, as above), or
// OK.
func (f *fake) Prompt(ctx context.Context, q acp.PromptRequest) (acp.PromptResponse, error) {
	var text strings.Builder
	for _, b := range q.Prompt {
		if b.Text != nil {
			text.WriteString(b.Text.Text)
		}
	}
	f.mu.Lock()
	s := f.sessions[q.SessionId]
	if s == nil {
		f.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidParams("no such session")
	}
	if s.busy { // one turn at a time, as an agent takes them
		f.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"error": "a prompt while the session's turn runs"})
	}
	s.busy = true
	s.bytes += text.Len()
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		s.busy = false
		f.mu.Unlock()
	}()
	if f.hang {
		<-ctx.Done()
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	say := func(u acp.SessionUpdate) {
		_ = f.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: q.SessionId, Update: u})
	}
	info := func(meta map[string]any) {
		say(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: meta}})
	}
	reply, stop := "OK", acp.StopReasonEndTurn
	var counted *usage.Usage
	t := text.String()
	if f.quota > 0 && len(t) > f.quota {
		t = "fake:failure:limit:"
	}
	switch {
	case f.relay != "":
		var err error
		if reply, counted, err = f.ask(ctx, s.static, t); err != nil {
			return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"error": err.Error()})
		}
	case strings.Contains(t, "fake:echo"):
		reply = f.echo(s, t)
	case strings.Contains(t, "fake:script:"):
		_, script, _ := strings.Cut(t, "fake:script:")
		script, _, _ = strings.Cut(script, "\n")
		replies := strings.Split(script, "|||")
		reply = replies[0]
		f.mu.Lock()
		s.replies = replies[1:]
		f.mu.Unlock()
	case strings.Contains(t, "fake:fence"):
		reply = "```json\n{\"a\": 1}\n```"
	case strings.Contains(t, "fake:spent"):
		reply, stop = `{"a": `, acp.StopReasonMaxTokens
	case strings.Contains(t, "fake:refusal"):
		reply, stop = "", acp.StopReasonRefusal
	case strings.Contains(t, "fake:login"):
		return acp.PromptResponse{}, acp.NewAuthRequired(nil)
	case strings.Contains(t, "fake:failure:"):
		_, what, _ := strings.Cut(t, "fake:failure:")
		category, action, _ := strings.Cut(strings.Fields(what)[0], ":")
		info(map[string]any{"jetbrains": map[string]any{"air": map[string]any{"sessionFailure": map[string]any{"id": "f1", "revision": 1,
			"category": category, "severity": "error", "title": "It failed", "details": "resets at 17:00", "actions": strings.Split(action, ",")}}}})
		reply = ""
	case strings.Contains(t, "fake:warning"):
		info(map[string]any{"jetbrains": map[string]any{"air": map[string]any{"sessionFailure": map[string]any{"id": "w1", "revision": 1,
			"category": "unknown", "severity": "warning", "title": "A notice", "actions": []string{}}}}})
	case strings.Contains(t, "fake:systemerror"):
		info(map[string]any{"codex": map[string]any{"threadStatus": map[string]any{"type": "systemError"}}})
		reply = ""
	case strings.Contains(t, "fake:crash"):
		os.Exit(3)
	case strings.Contains(t, "fake:logout"):
		f.mu.Lock()
		f.login = true
		f.mu.Unlock()
	case strings.Contains(t, "fake:tool"):
		reply = f.tool(ctx, q.SessionId, say)
	case strings.Contains(t, "fake:slow"):
		return slow(ctx)
	case strings.Contains(t, "fake:deaf"):
		time.Sleep(time.Hour) // till the process ends
		return acp.PromptResponse{}, nil
	case strings.Contains(t, "fake:big"):
		reply = strings.Repeat("x", 1100<<10)
	default:
		f.mu.Lock()
		if len(s.replies) > 0 {
			reply, s.replies = s.replies[0], s.replies[1:]
		}
		f.mu.Unlock()
		if reply == "fake:slow" {
			return slow(ctx)
		}
	}
	for _, part := range []string{reply[:len(reply)/2], reply[len(reply)/2:]} {
		if part != "" {
			say(acp.UpdateAgentMessageText(part))
		}
	}
	f.mu.Lock()
	in := f.overhead + int(f.scale*float64(s.bytes)/4)
	s.bytes += len(reply)
	f.mu.Unlock()
	res := acp.PromptResponse{StopReason: stop}
	if counted == nil && f.counts {
		counted = &usage.Usage{InTokens: in, OutTokens: len(reply) / 4}
	}
	if counted != nil {
		cached := counted.CachedTokens
		res.Usage = &acp.Usage{InputTokens: counted.InTokens - cached, CachedReadTokens: &cached, OutputTokens: counted.OutTokens,
			TotalTokens: counted.InTokens + counted.OutTokens}
	}
	if f.size > 0 && res.Usage != nil {
		say(acp.SessionUpdate{UsageUpdate: &acp.SessionUsageUpdate{Used: res.Usage.TotalTokens, Size: f.size}})
	}
	return res, nil
}

// echo is what session s and the process got, as JSON: its folder, its
// options, its instructions, the prompt, the agent's environment of note, the
// sessions opened and open, and whether the client declared AIR.
func (f *fake) echo(s *session, text string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	env := map[string]string{}
	for _, k := range []string{"CODEX_HOME", "CODEX_CONFIG", "DS_APIKEY", "DEEPCODE_CHAT_API_KEY", "PATH"} {
		env[k] = os.Getenv(k)
	}
	b, _ := json.Marshal(map[string]any{"cwd": s.cwd, "meta": s.meta, "static": s.static, "prompt": text, "config": s.config, "env": env,
		"pid": os.Getpid(), "opened": f.opened, "open": f.opened - f.closed, "air": f.air})
	return string(b)
}

// tool reports a tool call of the agent's own, asks for permission to run it,
// to read and write a file and to run a command, each named by more than 80
// characters, ending in Long (which the client's log leaves out), and says
// what it was told.
func (f *fake) tool(ctx context.Context, id acp.SessionId, say func(acp.SessionUpdate)) string {
	long := strings.Repeat("x", 80) + " Long"
	say(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "t1", Title: "Read " + long, Kind: acp.ToolKindRead}})
	title := "Run " + long
	res, err := f.conn.RequestPermission(ctx, acp.RequestPermissionRequest{SessionId: id, ToolCall: acp.ToolCallUpdate{ToolCallId: "t2", Title: &title},
		Options: []acp.PermissionOption{{OptionId: "yes", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "no", Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce}}})
	outcome := fmt.Sprint(err)
	if err == nil && res.Outcome.Selected != nil {
		outcome = string(res.Outcome.Selected.OptionId)
	} else if err == nil && res.Outcome.Cancelled != nil {
		outcome = "cancelled"
	}
	_, read := f.conn.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: id, Path: "/etc/" + long})
	_, _ = f.conn.WriteTextFile(ctx, acp.WriteTextFileRequest{SessionId: id, Path: "/etc/" + long, Content: "x"})
	_, _ = f.conn.CreateTerminal(ctx, acp.CreateTerminalRequest{SessionId: id, Command: long})
	return fmt.Sprintf("permission %s; read refused: %v", outcome, read != nil)
}

// ask asks the relay server for the answer to static and text, and returns
// what it counted.
func (f *fake) ask(ctx context.Context, static, text string) (string, *usage.Usage, error) {
	var counted usage.Usage
	c := &compat.Client{BaseURL: f.relay, ModelName: "fake-chat",
		Meter: usage.Func(func(_ context.Context, _ string, u usage.Usage) { counted = u })}
	reply, err := ai.Generate(ctx, c, ai.Prompt{Static: static, Dynamic: text})
	return reply, &counted, err
}

// Authenticate is not used: deepcode never signs an agent in.
func (f *fake) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

// Logout does nothing.
func (f *fake) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

// Cancel does nothing more than the connection, which ends the prompt's context.
func (f *fake) Cancel(context.Context, acp.CancelNotification) error { return nil }

// slow answers nothing till ctx, the prompt's, is cancelled, then ends the
// turn as cancelled a moment later, as an agent takes some time to stop.
func slow(ctx context.Context) (acp.PromptResponse, error) {
	waits("a slow prompt")
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
}

// waits says in the agent's log, its standard error, that the fake waits on
// what, for a test to wait till it does (Await).
func waits(what string) { fmt.Fprintf(os.Stderr, "fake: waits on %s\n", what) }

// Await returns once the fake whose agent.log is in dir waits on what (a
// stalled session/new, a slow prompt), failing t if it does not in 20 s.
func Await(t testing.TB, dir, what string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if b, _ := os.ReadFile(filepath.Join(dir, "agent.log")); strings.Contains(string(b), "fake: waits on "+what+"\n") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fake never waited on %s", what)
		}
	}
}

// ListSessions lists none.
func (f *fake) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{Sessions: []acp.SessionInfo{}}, nil
}

// ResumeSession does nothing.
func (f *fake) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

// SetSessionMode does nothing.
func (f *fake) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}
