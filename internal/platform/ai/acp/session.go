package acp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/usage"
)

// session is one session of a running agent, open until close, and the
// instructions its adapter took (meta), "" where the prompt holds them.
type session struct {
	a      *Agent
	r      *running
	id     acp.SessionId
	static string
}

// bare is the instructions of a session of Claude's that deepcode gives none:
// Claude's adapter takes an empty system prompt for none and gives its own,
// Claude Code's, in its place.
const bare = "Answer the request."

// result is what a prompt got: the answer's text, why it stopped, the tokens
// the agent counted (nil when it gave none).
type result struct {
	text  string
	stop  acp.StopReason
	usage *acp.Usage
}

// signIn is what an agent that is not signed in needs.
const signIn = "the agent is not signed in: sign it in as the OS user deepcode runs as (claude /login for Claude Code; codex login for Codex, " +
	"with CODEX_HOME set to acp_codex_home when that is set)"

// Ask streams the agent's answer to p, relaying its text as it comes, in a
// session of its own; with s, the answer is JSON, and loses the one fence
// around it when it has one (design 5.4). Reasoning is no part of the answer.
func (a *Agent) Ask(ctx context.Context, p ai.Prompt, s *ai.JSONSchema, onDelta func(string)) (_ ai.Reply, err error) {
	ctx, cancel := ai.WithTimeout(ctx, a.Timeout, a.Name())
	defer cancel()
	defer func() { err = ai.Cause(ctx, err) }()
	start := time.Now()
	ss, err := a.session(ctx, p.Static)
	if err != nil {
		a.meter(ctx, start, []string{p.Static}, result{}, err)
		return ai.Reply{}, err
	}
	text := ss.text(p)
	sent := []string{ss.static, text}
	res, err := ss.ask(ctx, text, onDelta)
	a.meter(ctx, start, sent, res, err) // before the session closes: its time is the answer's
	ss.close()
	if err != nil {
		return ai.Reply{Text: res.text}, err
	}
	if res.usage != nil && a.Count != nil {
		local := 0
		for _, t := range sent {
			local += a.Count(t)
		}
		a.observe(counted(res.usage).InTokens, local)
	}
	reply := ai.Reply{Text: res.text, Spent: res.stop == acp.StopReasonMaxTokens}
	if s != nil {
		reply.Text = unfence(reply.Text)
	}
	return reply, nil
}

// meter tells the Meter a request's Usage: the agent's count, or, for an
// answer it counted nothing for, the local tokenizer's estimate of sent, the
// texts the session held, and the answer; and its time since start.
func (a *Agent) meter(ctx context.Context, start time.Time, sent []string, res result, err error) {
	if a.Meter == nil {
		return
	}
	u := counted(res.usage)
	u.Elapsed, u.OutChars = time.Since(start), utf8.RuneCountInString(res.text)
	for _, s := range sent {
		u.InChars += utf8.RuneCountInString(s)
	}
	if err == nil {
		u.Estimate(a.Count, sent, []string{res.text})
	}
	a.Meter.Record(ctx, a.Name(), u)
}

// counted is the Usage of an agent's count, which, unlike OpenAI's, keeps the
// tokens read from and written to its cache out of its input.
func counted(u *acp.Usage) ai.Usage {
	if u == nil {
		return ai.Usage{}
	}
	read, written := 0, 0
	if u.CachedReadTokens != nil {
		read = *u.CachedReadTokens
	}
	if u.CachedWriteTokens != nil {
		written = *u.CachedWriteTokens
	}
	return ai.Usage{InTokens: u.InputTokens + read + written, CachedTokens: read, OutTokens: u.OutputTokens}
}

// session opens a session for a request with static as its instructions, on
// the running process, counted open on it until close.
func (a *Agent) session(ctx context.Context, static string) (*session, error) {
	r, err := a.agent(ctx)
	if err != nil {
		return nil, err
	}
	s, err := a.open(ctx, r, usage.Repository(ctx), static)
	if err != nil {
		a.release(r)
		return nil, err
	}
	return s, nil
}

// open opens a session on r in repository repo's folder, an empty one
// (<Dir>/work/<repo>, _ for none), with static as its instructions where the
// adapter takes them (meta), and sets its options (configure). r's count of
// open sessions is the caller's to keep, on a failure too.
func (a *Agent) open(ctx context.Context, r *running, repo int64, static string) (*session, error) {
	folder := "_"
	if repo > 0 {
		folder = strconv.FormatInt(repo, 10)
	}
	dir, err := filepath.Abs(filepath.Join(a.Dir, "work", folder))
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return nil, err
	}
	meta, taken := a.meta(r.kind, static)
	res, err := r.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: dir, McpServers: []acp.McpServer{}, Meta: meta})
	if err != nil {
		return nil, a.fail(r, "session/new", err)
	}
	s := &session{a: a, r: r, id: res.SessionId, static: taken}
	if err := a.configure(ctx, s, res.ConfigOptions); err != nil {
		s.end()
		return nil, err
	}
	return s, nil
}

// meta is a session's options for an adapter of kind (design 5.6), and the
// instructions it takes: Claude's takes static, else bare, as its whole system
// prompt and gets no tool, no suggested next prompt, and none of the user's
// settings, CLAUDE.md, MCP servers and plugins, nor the account's claude.ai
// connectors (which join from a session's second prompt otherwise) unless
// UserConfig and Connectors say so; Codex's takes static as its custom
// instructions; another takes none.
func (a *Agent) meta(kind, static string) (map[string]any, string) {
	switch kind {
	case "claude":
		sources := []string{}
		if a.UserConfig {
			sources = []string{"user", "project", "local"}
		}
		static = cmp.Or(static, bare)
		return map[string]any{"systemPrompt": static, "claudeCode": map[string]any{"options": map[string]any{
			"tools": []string{}, "settingSources": sources, "promptSuggestions": false, "strictMcpConfig": !a.UserConfig,
			"settings": map[string]any{"disableClaudeAiConnectors": !a.Connectors}}}}, static
	case "codex":
		return map[string]any{"jetbrains": map[string]any{"air": map[string]any{"customInstructions": static}}}, static
	}
	return nil, ""
}

// configure sets s's options: the model to Model and the thought level to
// Effort when set, each one of the values the agent lists for it, or the
// setting is wrong; and the mode to read-only where the agent has one. Each
// is checked by the options as the one set before left them, as an agent's
// thought levels may be the model's.
func (a *Agent) configure(ctx context.Context, s *session, options []acp.SessionConfigOption) error {
	for _, want := range []struct {
		category      acp.SessionConfigOptionCategory
		value, option string
	}{{acp.SessionConfigOptionCategoryModel, a.Model, "chat_model"}, {acp.SessionConfigOptionCategoryThoughtLevel, a.Effort, "reasoning_effort"},
		{acp.SessionConfigOptionCategoryMode, "read-only", ""}} {
		if want.value == "" {
			continue
		}
		i := slices.IndexFunc(options, func(o acp.SessionConfigOption) bool {
			return o.Select != nil && o.Select.Category != nil && *o.Select.Category == want.category
		})
		var values []string
		if i >= 0 {
			values = choices(options[i].Select.Options)
		}
		switch {
		case !slices.Contains(values, want.value) && want.option == "":
			continue // no read-only mode to choose
		case i < 0:
			return ai.Kinded(fmt.Errorf("%s: %s %q: the agent has no %s option", a.Name(), want.option, want.value, want.category), ai.ErrRefused)
		case !slices.Contains(values, want.value):
			return ai.Kinded(fmt.Errorf("%s: %s %q is not one of the agent's: %s", a.Name(), want.option, want.value, strings.Join(values, ", ")), ai.ErrRefused)
		case string(options[i].Select.CurrentValue) == want.value:
			continue
		}
		res, err := s.r.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: s.id, ConfigId: options[i].Select.Id, Value: acp.SessionConfigValueId(want.value)}})
		if err != nil {
			return a.fail(s.r, "session/set_config_option", err)
		}
		if len(res.ConfigOptions) > 0 {
			options = res.ConfigOptions
		}
	}
	return nil
}

// choices are the values a select option offers, grouped or not.
func choices(o acp.SessionConfigSelectOptions) []string {
	var all []acp.SessionConfigSelectOption
	if o.Ungrouped != nil {
		all = *o.Ungrouped
	}
	if o.Grouped != nil {
		for _, g := range *o.Grouped {
			all = append(all, g.Options...)
		}
	}
	values := make([]string, len(all))
	for i, c := range all {
		values[i] = string(c.Value)
	}
	return values
}

// close ends s and counts it closed on its process.
func (s *session) close() {
	s.end()
	s.a.release(s.r)
}

// end closes s where the agent offers that.
func (s *session) end() {
	if s.r.closes {
		ctx, cancel := context.WithTimeout(context.Background(), grace)
		_, _ = s.r.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: s.id})
		cancel()
	}
}

// text is p as the session's prompt: its instructions first, in an
// instructions element, where the adapter took none (meta), then the rest
// (transcript).
func (s *session) text(p ai.Prompt) string {
	if s.r.kind == "" && p.Static != "" {
		return "<instructions>\n" + p.Static + "\n</instructions>\n\n" + transcript(p)
	}
	return transcript(p)
}

// transcript is p's conversation so far, written out, as no message of the
// agent's can be made up, then what is new.
func transcript(p ai.Prompt) string {
	var b strings.Builder
	if len(p.Previous) > 0 {
		b.WriteString("<conversation>\n")
		for _, m := range p.Previous {
			fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n", m.Role, m.Content, m.Role)
		}
		b.WriteString("</conversation>\n\n")
	}
	b.WriteString(p.Dynamic)
	return b.String()
}

// ask prompts s with text under a turn of Sessions and RPM, relaying the
// answer to onDelta, and fails as design 5.5 says: a failure the agent typed
// (failed), Codex's silent system error, which retires the process, a stop
// for a refusal or for too many turns.
func (s *session) ask(ctx context.Context, text string, onDelta func(string)) (result, error) {
	select {
	case s.a.slots <- struct{}{}:
	case <-ctx.Done():
		return result{}, ctx.Err()
	}
	defer func() { <-s.a.slots }()
	if err := s.a.turns.Wait(ctx, s.a.RPM); err != nil {
		return result{}, err
	}
	t := &turn{answer: &ai.Answer{OnDelta: onDelta}}
	s.r.cl.watch(s.id, t)
	res, err := s.r.conn.Prompt(ctx, acp.PromptRequest{SessionId: s.id, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	s.r.cl.forget(s.id)
	t.mu.Lock()
	defer t.mu.Unlock()
	out := result{text: t.answer.String(), stop: res.StopReason, usage: res.Usage}
	if t.size > 0 {
		s.a.mu.Lock()
		s.a.size = t.size
		s.a.mu.Unlock()
	}
	name := s.a.Name()
	switch {
	case err != nil:
		return out, s.a.fail(s.r, "session/prompt", err)
	case t.err != nil:
		return out, t.err
	case t.failure != nil:
		return out, s.a.failed(*t.failure)
	case t.broken && strings.TrimSpace(out.text) == "":
		s.a.retire(s.r)
		return out, ai.Kinded(fmt.Errorf("%s: the agent's thread failed in a system error and gave no answer", name), ai.ErrUnavailable)
	case res.StopReason == acp.StopReasonEndTurn || res.StopReason == acp.StopReasonMaxTokens:
		return out, nil
	case res.StopReason == acp.StopReasonCancelled && ctx.Err() != nil:
		return out, ctx.Err()
	}
	return out, ai.Kinded(fmt.Errorf("%s: the agent stopped its answer: %s", name, res.StopReason), ai.ErrFailed)
}

// fail is err, which step got from r, fit to return: an ai.ErrUnavailable
// when r has ended, an ai.ErrRefused when the agent is not signed in, ctx's
// end as it is, and an ai.ErrFailed otherwise.
func (a *Agent) fail(r *running, step string, err error) error {
	var re *acp.RequestError
	switch {
	case r.ended():
		a.retire(r)
		return ai.Kinded(fmt.Errorf("%s %s: the agent ended: %w (its log: %s)", a.Name(), step, err, filepath.Join(a.Dir, "agent.log")), ai.ErrUnavailable)
	case errors.As(err, &re) && re.Code == -32000: // Authentication required
		return ai.Kinded(fmt.Errorf("%s %s: %s", a.Name(), step, signIn), ai.ErrRefused)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return err
	}
	return ai.Kinded(fmt.Errorf("%s %s: %w", a.Name(), step, err), ai.ErrFailed)
}

// failed is the error of a failure the agent typed (design 5.5): signing in
// is refused; a context or budget a new session would clear is an
// ai.ErrContext; a quota, a rate limit, a service's or a connection's failure
// may pass, so a later try may get through; anything else failed the answer.
func (a *Agent) failed(f failure) error {
	words := f.Title
	if f.Details != "" {
		words += ": " + f.Details
	}
	err := fmt.Errorf("%s: %s", a.Name(), words)
	switch f.Category {
	case "access":
		return ai.Kinded(fmt.Errorf("%w: %s", err, signIn), ai.ErrRefused)
	case "limit":
		if slices.Contains(f.Actions, "new_session") {
			return &ai.ErrContext{Err: err}
		}
		return ai.Kinded(err, ai.ErrUnavailable)
	case "service", "connection":
		return ai.Kinded(err, ai.ErrUnavailable)
	}
	return ai.Kinded(err, ai.ErrFailed)
}

// unfence is text without the one fence around it, when it has exactly one:
// trimmed, it opens and closes with ```, and holds no other (design 5.4, D8).
func unfence(text string) string {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") || strings.Count(t, "```") != 2 {
		return text
	}
	_, inner, ok := strings.Cut(t, "\n")
	if !ok {
		return text // one line: no fence to take off it
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(inner), "```"))
}
