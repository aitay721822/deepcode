package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"deepcode/internal/platform/ai"
)

// exchange is an ai.Exchange held by the agent (design 5.3, plan A): one
// session from the first request to Close, in which the model calls tools by
// answering in JSON, its calls checked by a schema made of theirs, and each
// request sends the results of the reply before it. The agent keeps the
// conversation, so no result is masked once sent (Compact).
type exchange struct {
	ai.Pieces
	a       *Agent
	p       ai.Prompt
	format  ai.Format[calls]
	bad     error    // the tools gave no format
	s       *session // from the first request on
	pending []string // what the next request sends: the first prompt, then the results
	local   int      // the local tokenizer's count of the pieces (Agent.Count)
	calls   int      // the calls so far, which number the next one's Key
	used    ai.Usage // the last request's
	closed  bool
}

// calls is a reply of the exchange: the tools it calls, an empty list when
// it calls none.
type calls struct {
	Calls []struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"calls"`
}

// callsLine heads the tools in an exchange's instructions.
const callsLine = `You call tools by replying with only JSON: {"calls": [{"tool": "<name>", "arguments": {...}}]}, ` +
	"the calls you make this turn, or an empty list once you make none. Each call's result comes back in a <result> element. The tools:"

// Exchange starts a conversation from p in which the model may call tools:
// its instructions end with the tools, the reply's format and its schema
// (ai.Format.Prompt), each tool's arguments a branch of it.
func (a *Agent) Exchange(p ai.Prompt, tools []ai.Tool) ai.Exchange {
	e := &exchange{a: a}
	var branches []any
	var list strings.Builder
	list.WriteString(callsLine)
	for _, t := range tools {
		fmt.Fprintf(&list, "\n- %s: %s", t.Name, t.Description)
		branches = append(branches, map[string]any{"type": "object", "additionalProperties": false, "required": []string{"tool", "arguments"},
			"properties": map[string]any{"tool": map[string]any{"type": "string", "const": t.Name}, "arguments": t.JSON}})
	}
	e.format, e.bad = ai.Dynamic[calls]("calls", map[string]any{"type": "object", "additionalProperties": false, "required": []string{"calls"},
		"properties": map[string]any{"calls": map[string]any{"type": "array", "items": map[string]any{"anyOf": branches}}}})
	p.Static = strings.TrimSpace(p.Static + "\n\n" + list.String())
	e.p = e.format.Prompt(a, p)
	e.add(e.p.Static)
	e.add(transcript(e.p)) // the instructions are a piece whether the adapter takes them or the prompt holds them
	return e
}

// add keeps s as the next piece, counted locally.
func (e *exchange) add(s string) {
	e.Add(s)
	if e.a.Count != nil {
		e.local += e.a.Count(s)
	}
}

// Next asks for the model's next reply and returns the tools it calls; its
// text is the JSON of them, so none is returned. The first request opens the
// session. A reply that is not the JSON asked for is asked again once in the
// same session, with the validator's words; one that still is not fails
// with ai.ErrFailed, one cut at the output budget with ai.ErrSpent.
func (e *exchange) Next(ctx context.Context) (_ string, out []ai.ToolCall, err error) {
	switch {
	case e.closed:
		return "", nil, errors.New("the exchange is closed")
	case e.bad != nil:
		return "", nil, e.bad
	}
	ctx, cancel := ai.WithTimeout(ctx, e.a.Timeout, e.a.Name())
	defer cancel()
	defer func() { err = ai.Cause(ctx, err) }()
	if e.s == nil {
		start := time.Now()
		if e.s, err = e.a.session(ctx, e.p.Static); err != nil {
			e.a.meter(ctx, start, e.Texts(e.Len()), result{}, err)
			return "", nil, err
		}
		e.pending = []string{e.s.text(e.p)}
	}
	text := strings.Join(e.pending, "\n\n")
	sent := e.Len()
	reply, res, err := e.ask(ctx, text, sent)
	if err != nil {
		return "", nil, err // pending kept: a Next after it sends it again
	}
	e.pending = nil
	if res.stop == acp.StopReasonMaxTokens {
		return "", nil, ai.ErrSpent
	}
	v, bad := ai.Decode(e.format, unfence(reply))
	if bad != nil {
		e.add(reply)
		again := fmt.Sprintf("Your reply is not the JSON asked for (%v). Reply with only that JSON.", bad)
		e.add(again)
		sent, e.pending = e.Len(), []string{again}
		if reply, res, err = e.ask(ctx, again, sent); err != nil {
			return "", nil, err
		}
		e.pending = nil
		if res.stop == acp.StopReasonMaxTokens {
			return "", nil, ai.ErrSpent
		}
		if v, bad = ai.Decode(e.format, unfence(reply)); bad != nil {
			return "", nil, ai.Kinded(fmt.Errorf("%s: the reply is %w", e.a.Name(), bad), ai.ErrFailed)
		}
	}
	e.MarkSent(sent)
	e.add(reply)
	for _, c := range v.Calls {
		out = append(out, ai.ToolCall{ID: strconv.Itoa(e.calls), Name: c.Tool, Arguments: string(c.Arguments), Key: e.calls})
		e.calls++
	}
	return "", out, nil
}

// ask prompts the session with text, the pieces up to sent being what it
// holds once text is sent, and meters the request.
func (e *exchange) ask(ctx context.Context, text string, sent int) (string, result, error) {
	start := time.Now()
	res, err := e.s.ask(ctx, text, nil)
	e.a.meter(ctx, start, e.Texts(sent), res, err)
	e.used = counted(res.usage)
	if err == nil {
		e.a.observe(e.used.InTokens, e.local)
	}
	return res.text, res, err
}

// Answer gives the model result as what call returned, sent with the next
// request.
func (e *exchange) Answer(call ai.ToolCall, result string) {
	r := fmt.Sprintf("<result call=\"%d\" tool=%q>\n%s\n</result>", call.Key, call.Name, result)
	e.pending = append(e.pending, r)
	e.add(r)
}

// Compact replaces nothing: the agent holds what was sent.
func (e *exchange) Compact(map[int]string) int { return 0 }

// Used is the last request's Usage: InTokens 0 when the agent counted none.
func (e *exchange) Used() ai.Usage { return e.used }

// Close closes the session.
func (e *exchange) Close() {
	if e.s != nil && !e.closed {
		e.s.close()
	}
	e.closed = true
}
