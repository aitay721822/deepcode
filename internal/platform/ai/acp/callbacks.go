package acp

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"deepcode/internal/platform/ai"
)

// air declares the extension the Claude and Codex adapters share (JetBrains'
// "AIR" metadata): a failure comes as a typed session update rather than as
// text in the answer (Codex sends its error as the answer otherwise), and a
// session's instructions come in session/new.
var air = map[string]any{"jetbrains": map[string]any{"air": map[string]any{"version": 1,
	"capabilities": []string{"sessionFailure", "customInstructions"}}}}

// client is deepcode's side of an agent's connection: it hands each session's
// updates to the prompt waiting on it, refuses every permission the agent
// asks for, and has no file system and no terminal to offer (design 5.6).
type client struct {
	name  string // the agent's, for the log
	mu    sync.Mutex
	turns map[acp.SessionId]*turn
}

// turn is what a session's prompt has had from the agent so far.
type turn struct {
	mu      sync.Mutex
	answer  *ai.Answer
	err     error    // the answer's own: past its cap
	size    int      // the context's tokens, as a usage update gave them
	failure *failure // the failure the agent reported
	broken  bool     // Codex's thread in a system error, of which it says nothing more
}

// failure is a session's failure as the AIR extension types it: a category
// (access, limit, service, connection, request, unknown) and the actions that
// may recover from it (login, retry, new_session); only an error's counts, a
// warning being a notice.
type failure struct {
	Category string   `json:"category"`
	Severity string   `json:"severity"`
	Title    string   `json:"title"`
	Details  string   `json:"details"`
	Actions  []string `json:"actions"`
}

// watch hands session id's updates to t until forget.
func (c *client) watch(id acp.SessionId, t *turn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.turns[id] = t
}

// forget stops handing session id's updates on.
func (c *client) forget(id acp.SessionId) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.turns, id)
}

// SessionUpdate hands an update to its session's prompt: the answer's text,
// the context's size, a failure; the agent's reasoning is no part of the
// answer, and a tool the agent calls of its own is logged, as it gets none.
func (c *client) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	t := c.turns[n.SessionId]
	c.mu.Unlock()
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch u := n.Update; {
	case u.AgentMessageChunk != nil:
		if text := u.AgentMessageChunk.Content.Text; text != nil && t.err == nil {
			t.err = t.answer.Add(text.Text)
		}
	case u.ToolCall != nil:
		log.Printf("acp: %s called a tool of its own, %.80q (%s), in a session that has none", c.name, u.ToolCall.Title, u.ToolCall.Kind) // cut: it may quote the prompt
	case u.UsageUpdate != nil:
		t.size = u.UsageUpdate.Size
	case u.SessionInfoUpdate != nil:
		var m struct {
			JetBrains struct {
				AIR struct {
					Failure *failure `json:"sessionFailure"`
				} `json:"air"`
			} `json:"jetbrains"`
			Codex struct {
				Thread struct {
					Type string `json:"type"`
				} `json:"threadStatus"`
			} `json:"codex"`
		}
		b, _ := json.Marshal(u.SessionInfoUpdate.Meta)
		if json.Unmarshal(b, &m) == nil {
			if f := m.JetBrains.AIR.Failure; f != nil && f.Severity == "error" {
				t.failure = f
			}
			t.broken = t.broken || m.Codex.Thread.Type == "systemError"
		}
	}
	return nil
}

// RequestPermission refuses, logging what was asked: once when the agent
// offers that, else by cancelling.
func (c *client) RequestPermission(_ context.Context, q acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	title := ""
	if q.ToolCall.Title != nil {
		title = *q.ToolCall.Title
	}
	log.Printf("acp: %s asked for permission to %.80q: refused", c.name, title)
	for _, o := range q.Options {
		if o.Kind == acp.PermissionOptionKindRejectOnce {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId}}}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
}

// errNoAccess refuses the files and terminals deepcode does not offer.
var errNoAccess = errors.New("deepcode offers an agent no file access and no terminal")

// ReadTextFile refuses, logging the path.
func (c *client) ReadTextFile(_ context.Context, q acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	log.Printf("acp: %s asked to read %.80q: refused", c.name, q.Path)
	return acp.ReadTextFileResponse{}, errNoAccess
}

// WriteTextFile refuses, logging the path.
func (c *client) WriteTextFile(_ context.Context, q acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	log.Printf("acp: %s asked to write %.80q: refused", c.name, q.Path)
	return acp.WriteTextFileResponse{}, errNoAccess
}

// CreateTerminal refuses, logging the command.
func (c *client) CreateTerminal(_ context.Context, q acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	log.Printf("acp: %s asked to run %.80q: refused", c.name, q.Command)
	return acp.CreateTerminalResponse{}, errNoAccess
}

// KillTerminal refuses: there is no terminal.
func (c *client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errNoAccess
}

// TerminalOutput refuses: there is no terminal.
func (c *client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errNoAccess
}

// ReleaseTerminal refuses: there is no terminal.
func (c *client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errNoAccess
}

// WaitForTerminalExit refuses: there is no terminal.
func (c *client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errNoAccess
}
