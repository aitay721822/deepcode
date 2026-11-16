package acpprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// agent is one agent process, its connection, and what it said when
// initialized; label names its stop finding.
type agent struct {
	label  string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *os.File
	conn   *acp.ClientSideConnection
	rec    *recorder
	init   acp.InitializeResponse
	once   sync.Once
}

// recorder is the client side of the connection: it keeps each session's
// updates, refuses every permission and file request, and runs no terminal.
type recorder struct {
	mu sync.Mutex
	by map[acp.SessionId]*turn
}

// turn is what one session has had from the agent so far; a prompt's part of
// it lies past the mark taken before the prompt.
type turn struct {
	text                                   strings.Builder // the current prompt's answer
	first                                  time.Time       // its first chunk's arrival
	thought                                int             // bytes of reasoning
	tools, updates, perms, files, commands []string
	usage                                  []acp.SessionUsageUpdate
	other                                  map[string]int // updates of other kinds, by kind
	raw                                    []string       // the first 40 of those a prompt, as JSON cut short
	rawMax                                 int            // how long raw may grow in the current prompt
}

// mark is where a session's updates stood before a prompt.
type mark struct {
	thought, tools, updates, perms, files, commands, usage, raw int
	other                                                       map[string]int
}

// of is session id's turn, made on first use; r.mu must be held.
func (r *recorder) of(id acp.SessionId) *turn {
	t := r.by[id]
	if t == nil {
		t = &turn{other: map[string]int{}, rawMax: 40}
		r.by[id] = t
	}
	return t
}

// mark is where t stands now.
func (t *turn) mark() mark {
	return mark{t.thought, len(t.tools), len(t.updates), len(t.perms), len(t.files), len(t.commands), len(t.usage), len(t.raw), maps.Clone(t.other)}
}

// SessionUpdate keeps an update: the answer's text, the reasoning's size,
// tool calls and their outcomes, usage, and how many of every other kind.
func (r *recorder) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, u := r.of(n.SessionId), n.Update
	switch {
	case u.AgentMessageChunk != nil:
		if c := u.AgentMessageChunk.Content.Text; c != nil {
			if t.text.Len() == 0 {
				t.first = time.Now()
			}
			t.text.WriteString(c.Text)
		}
	case u.AgentThoughtChunk != nil:
		if c := u.AgentThoughtChunk.Content.Text; c != nil {
			t.thought += len(c.Text)
		}
	case u.ToolCall != nil:
		t.tools = append(t.tools, cut(fmt.Sprintf("%s [%s] %s", u.ToolCall.Title, u.ToolCall.Kind, mustJSON(u.ToolCall.RawInput)), 600))
	case u.ToolCallUpdate != nil:
		t.updates = append(t.updates, cut(mustJSON(u.ToolCallUpdate), 600))
	case u.UsageUpdate != nil:
		t.usage = append(t.usage, *u.UsageUpdate)
	default:
		b, _ := json.Marshal(u)
		var m struct {
			Kind string `json:"sessionUpdate"`
		}
		_ = json.Unmarshal(b, &m)
		t.other[m.Kind]++
		if len(t.raw) < t.rawMax {
			t.raw = append(t.raw, cut(string(b), 800))
		}
	}
	return nil
}

// RequestPermission refuses: once when the agent offers that, else cancelled.
func (r *recorder) RequestPermission(_ context.Context, q acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	r.mu.Lock()
	t := r.of(q.SessionId)
	t.perms = append(t.perms, cut(mustJSON(q.ToolCall), 600))
	r.mu.Unlock()
	for _, o := range q.Options {
		if o.Kind == acp.PermissionOptionKindRejectOnce {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId}}}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
}

// ReadTextFile refuses, noting the path: the client offers no file access.
func (r *recorder) ReadTextFile(_ context.Context, q acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	r.mu.Lock()
	r.of(q.SessionId).files = append(r.of(q.SessionId).files, "read "+q.Path)
	r.mu.Unlock()
	return acp.ReadTextFileResponse{}, fmt.Errorf("no file access")
}

// WriteTextFile refuses, noting the path.
func (r *recorder) WriteTextFile(_ context.Context, q acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	r.mu.Lock()
	r.of(q.SessionId).files = append(r.of(q.SessionId).files, "write "+q.Path)
	r.mu.Unlock()
	return acp.WriteTextFileResponse{}, fmt.Errorf("no file access")
}

// CreateTerminal refuses, noting the command: the client offers no terminal.
func (r *recorder) CreateTerminal(_ context.Context, q acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	r.mu.Lock()
	r.of(q.SessionId).commands = append(r.of(q.SessionId).commands, q.Command)
	r.mu.Unlock()
	return acp.CreateTerminalResponse{}, fmt.Errorf("no terminal")
}

// KillTerminal refuses: there is no terminal.
func (r *recorder) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, fmt.Errorf("no terminal")
}

// TerminalOutput refuses: there is no terminal.
func (r *recorder) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("no terminal")
}

// ReleaseTerminal refuses: there is no terminal.
func (r *recorder) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("no terminal")
}

// WaitForTerminalExit refuses: there is no terminal.
func (r *recorder) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("no terminal")
}

// result is one prompt's turn as the probe saw it: SentTokens and
// ReplyTokens are o200k's count of the text sent (the instructions too, when
// a session's options carried them) and of the answer.
type result struct {
	Step, Case                string
	Session                   acp.SessionId `json:",omitempty"`
	Text                      string
	Stop                      acp.StopReason
	Usage                     *acp.Usage
	Updates                   []acp.SessionUsageUpdate
	Thought                   int
	Tools, ToolUpdates, Perms []string
	Files, Commands, Raw      []string
	Other                     map[string]int
	NewMs, FirstMs, TotalMs   int64
	SentTokens, ReplyTokens   int
	Err                       string `json:",omitempty"`
}

// carries reports whether a session's options carry its instructions:
// Claude's adapter takes them as its system prompt, Codex's as custom
// instructions; plain sessions and other agents get them in the prompt.
func (p *prober) carries(plain bool) bool { return !plain && p.kind != "other" }

// meta is a session's options as design 5.6 sets them: Claude's adapter gets
// static as its whole system prompt, no tool, no setting of the user's, no
// suggested next prompt, and no MCP server: none of the user's or a plugin's
// (strictMcpConfig) and none of the account's claude.ai connectors, which
// otherwise join from a session's second prompt; Codex's gets static as
// custom instructions.
func (p *prober) meta(static string, plain bool) map[string]any {
	switch {
	case !p.carries(plain):
		return nil
	case p.kind == "claude":
		return map[string]any{"systemPrompt": static, "claudeCode": map[string]any{"options": map[string]any{
			"tools": []string{}, "settingSources": []string{}, "promptSuggestions": false, "strictMcpConfig": true,
			"settings": map[string]any{"disableClaudeAiConnectors": true}}}}
	}
	return map[string]any{"jetbrains": map[string]any{"air": map[string]any{"customInstructions": static}}}
}

// text is what a session's first prompt sends: dynamic, after static as
// instructions when the session's options do not carry them.
func (p *prober) text(static, dynamic string, plain bool) string {
	if p.carries(plain) || static == "" {
		return dynamic
	}
	return "<instructions>\n" + static + "\n</instructions>\n\n" + dynamic
}

// usage is the usage updates session id has had so far (S4: whether
// session/new sends one before any prompt).
func (a *agent) usage(id acp.SessionId) []acp.SessionUsageUpdate {
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	return slices.Clone(a.rec.of(id).usage)
}

// open makes a session in cwd with static's options, and says how long it took.
func (p *prober) open(ctx context.Context, a *agent, cwd, static string, plain bool) (acp.NewSessionResponse, int64, error) {
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		return acp.NewSessionResponse{}, 0, err
	}
	began := time.Now()
	s, err := a.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}, Meta: p.meta(static, plain)})
	return s, time.Since(began).Milliseconds(), err
}

// send prompts session id with text and returns that prompt's part of the
// session's updates, copied.
func (p *prober) send(ctx context.Context, a *agent, id acp.SessionId, text string) result {
	a.rec.mu.Lock()
	t := a.rec.of(id)
	m := t.mark()
	t.text.Reset()
	t.first, t.rawMax = time.Time{}, len(t.raw)+40
	a.rec.mu.Unlock()
	began := time.Now()
	resp, err := a.conn.Prompt(ctx, acp.PromptRequest{SessionId: id, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	total := time.Since(began).Milliseconds()
	a.rec.mu.Lock()
	r := result{Session: id, Text: t.text.String(), Stop: resp.StopReason, Usage: resp.Usage, TotalMs: total, Thought: t.thought - m.thought,
		Updates: slices.Clone(t.usage[m.usage:]), Tools: slices.Clone(t.tools[m.tools:]), ToolUpdates: slices.Clone(t.updates[m.updates:]),
		Perms: slices.Clone(t.perms[m.perms:]), Files: slices.Clone(t.files[m.files:]), Commands: slices.Clone(t.commands[m.commands:]),
		Raw: slices.Clone(t.raw[m.raw:]), Other: map[string]int{}}
	for k, n := range t.other {
		if d := n - m.other[k]; d > 0 {
			r.Other[k] = d
		}
	}
	if !t.first.IsZero() {
		r.FirstMs = t.first.Sub(began).Milliseconds()
	}
	a.rec.mu.Unlock()
	if err != nil {
		r.Err = err.Error()
	}
	r.SentTokens, r.ReplyTokens = p.tokens(text), p.tokens(r.Text)
	return r
}

// ask is one request as design 5.2 sends it: a new session in the step's
// folder, one prompt, closed after.
func (p *prober) ask(ctx context.Context, a *agent, step, name, static, dynamic string) result {
	return p.askIn(ctx, a, step, name, p.folder(step), static, dynamic, false)
}

// folder is a step's sessions' folder.
func (p *prober) folder(parts ...string) string {
	return strings.Join(append([]string{p.work}, parts...), string(os.PathSeparator))
}

// askIn is ask in cwd; a plain session gets no options of the adapter's.
func (p *prober) askIn(ctx context.Context, a *agent, step, name, cwd, static, dynamic string, plain bool) result {
	s, ms, err := p.open(ctx, a, cwd, static, plain)
	if err != nil {
		return result{Step: step, Case: name, NewMs: ms, Err: "session/new: " + err.Error()}
	}
	r := p.send(ctx, a, s.SessionId, p.text(static, dynamic, plain))
	r.Step, r.Case, r.NewMs = step, name, ms
	if p.carries(plain) {
		r.SentTokens += p.tokens(static)
	}
	p.close(a, s.SessionId)
	return r
}

// close ends session id when the agent offers session/close.
func (p *prober) close(a *agent, id acp.SessionId) {
	if a.init.AgentCapabilities.SessionCapabilities.Close == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := a.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: id}); err != nil {
		p.emit("close.error", err.Error())
	}
}

// unfence is text trimmed, with one surrounding ``` fence taken off when it
// has one, and whether it had.
func unfence(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") || !strings.HasSuffix(text, "```") || strings.Count(text, "```") != 2 {
		return text, false
	}
	_, inner, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(inner), "```")), true
}

// decoded is r with whether its text is the JSON asked for, as it came and
// with its fence taken off.
func decoded(r result, decode func(string) error) map[string]any {
	err := decode(strings.TrimSpace(r.Text))
	inner, fenced := unfence(r.Text)
	unfenced := err == nil || fenced && decode(inner) == nil
	return map[string]any{"result": r, "decodes": err == nil, "decodeError": fmt.Sprint(err), "fenced": fenced, "decodesUnfenced": unfenced}
}
