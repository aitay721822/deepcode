// Package ai is what deepcode asks a chat model through, whatever reaches
// it: a backend implements Chat (package compat asks an OpenAI-compatible
// server). It holds what every backend shares: one order for every request's
// messages (Prompt), tools and their calls, errors that say whether a later
// try may get through (ErrUnavailable, ErrRefused, ErrFailed, ErrContext),
// the 1 MB answer cap (Answer), timeouts that name DEEPCODE_LLM_TIMEOUT, the
// text an exchange's next request sends (Pieces), and schemas, made from Go
// types or at request time within OpenAI's structured-output limits, the
// strictest a backend has, checked on the answer.
// The free functions Stream, Generate and StreamJSON ask any Chat.
package ai

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"deepcode/internal/platform/ai/usage"
)

// Message is one message of a request: its role (system, user or assistant)
// and its text.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Prompt is a request's messages in the order every request sends them, so a
// model server's prompt cache holds the longest prefix requests share: Static,
// the instructions the same on every request of its kind, as the system
// message; Previous, the conversation so far; Dynamic, what is new to this
// request, as the last user message.
type Prompt struct {
	Static   string
	Previous []Message
	Dynamic  string
}

// Messages are p's in that order, an empty Static or Dynamic left out.
func (p Prompt) Messages() []Message {
	var out []Message
	if p.Static != "" {
		out = append(out, Message{Role: "system", Content: p.Static})
	}
	out = append(out, p.Previous...)
	if p.Dynamic != "" {
		out = append(out, Message{Role: "user", Content: p.Dynamic})
	}
	return out
}

// Size is p's messages in count's units, and how many there are.
func (p Prompt) Size(count func(string) int) (size, messages int) {
	for _, m := range p.Messages() {
		size, messages = size+count(m.Content), messages+1
	}
	return size, messages
}

// After is the prompt that goes on from p once the model answered reply: p's
// Dynamic and reply join Previous, and dynamic is what is new.
func (p Prompt) After(reply, dynamic string) Prompt {
	previous := Prompt{Previous: p.Previous, Dynamic: p.Dynamic}.Messages() // a copy: p's own stays as it was
	return Prompt{Static: p.Static, Previous: append(previous, Message{Role: "assistant", Content: reply}), Dynamic: dynamic}
}

// Chat is a chat model, however it is reached. A nil Chat means none is
// configured. A backend tells its Meter every request's Usage, a failed one
// too: its tokens as the model counted them, or, for an answer it counted
// none for, estimated (Usage.Estimate); and its time. Callers meter nothing. It fails
// a request with the errors below: ErrUnavailable when a later try may get
// through, ErrRefused when a setting is wrong, ErrFailed when the model failed
// its answer, ErrContext when the request is longer than the context.
type Chat interface {
	// Name is the model's name in errors, logs and usage rows.
	Name() string
	// Ask streams the model's answer to p, relaying its text to onDelta as it
	// comes; with s, asking for s's JSON as SchemaMode says. A failed answer
	// keeps the text that came before it failed. An answer that stopped at the
	// output budget is no error but Spent; Stream and StreamJSON fail one with
	// nothing said, or JSON cut short.
	Ask(ctx context.Context, p Prompt, s *JSONSchema, onDelta func(string)) (Reply, error)
	// Exchange starts a conversation from p in which the model may call tools.
	Exchange(p Prompt, tools []Tool) Exchange
	// Window is the model's context as known now.
	Window() Window
	// Learn keeps context, the tokens a server said the model's context holds
	// when it refused a longer request (ErrContext), for as long as the
	// process runs.
	Learn(context int)
	// SchemaMode is where a request for JSON gives its schema: SchemaServer or
	// SchemaPrompt.
	SchemaMode() string
}

// Reply is an answer Ask gave: its text, and whether it stopped at the output
// budget.
type Reply struct {
	Text  string
	Spent bool
}

// Exchange is a conversation in which the model may call tools. It keeps the
// text the next request sends, piece by piece, for its caller to count in the
// model's tokens (Size); a call is known by its Key, as two calls may share an
// id.
type Exchange interface {
	// Next asks for the model's next reply: its text and the tools it calls,
	// each of which Answer answers before the next reply is asked for. A reply
	// that stopped at the output budget fails with ErrSpent, as its calls may
	// be cut. A failed request adds nothing to the exchange.
	Next(ctx context.Context) (text string, calls []ToolCall, err error)
	// Answer gives the model result as what call returned.
	Answer(call ToolCall, result string)
	// Compact gives the calls short, by Key, the text short gives them in
	// place of their results, and returns how many it replaced: none where
	// the backend cannot change what was sent.
	Compact(short map[int]string) int
	// Size is the next request's text in count's units, each piece counted
	// once (count must not change), and the part of it the last request sent.
	Size(count func(string) int) (next, sent int)
	// Added is the pieces the last request answered did not send.
	Added() int
	// Used is the last reply's Usage: InTokens 0 when the model counted none.
	Used() Usage
}

// Window is a model's context, as the backend knows it: Context tokens in
// all, 0 when unknown; Room of them kept for the answer, the output limit set
// or DefaultOutput; Ratio, the share of Context a request may fill before it
// is made smaller, 0 leaving it to the caller's default; Overhead, the tokens
// the backend adds to every request that deepcode does not send (an agent's
// own instructions and tools); and Scale, the model's tokens over the local
// tokenizer's for the same text, at least 1 (0 is taken as 1).
type Window struct {
	Context  int
	Room     int
	Ratio    float64
	Overhead int
	Scale    float64
}

// DefaultOutput is the room, in tokens, kept in the context for an answer
// whose size the settings leave to the server.
const DefaultOutput = 8192

// Tool is a function the model may call (Exchange), with its arguments in a
// Format's JSON: the function bears the Format's name.
type Tool struct {
	Description string
	JSONSchema
}

// Tool is the function f.Name, called with f's JSON, which Decode reads.
func (f Format[T]) Tool(description string) Tool { return Tool{description, f.JSONSchema} }

// ToolCall is a call the model made: its id, the function's name and its
// arguments as the model wrote them, and its Key, the calls before it in its
// Exchange: unique where an id may not be.
type ToolCall struct {
	ID, Name, Arguments string
	Key                 int
}

// Usage is one answer's size, as package usage reads it from each API, and
// Meter what a backend tells it to.
type (
	Usage = usage.Usage
	Meter = usage.Meter
)

// Generate returns c's whole answer to p.
func Generate(ctx context.Context, c Chat, p Prompt) (string, error) {
	return Stream(ctx, c, p, nil)
}

// Stream streams c's answer to p and relays its text as it comes. Reasoning
// is no part of the answer.
func Stream(ctx context.Context, c Chat, p Prompt, onDelta func(string)) (string, error) {
	r, err := ask(ctx, c, p, nil, onDelta)
	return r.Text, err
}

// ask is c.Ask, failing an answer that stopped at the output budget with
// nothing said.
func ask(ctx context.Context, c Chat, p Prompt, s *JSONSchema, onDelta func(string)) (Reply, error) {
	r, err := c.Ask(ctx, p, s, onDelta)
	if err == nil && r.Spent && strings.TrimSpace(r.Text) == "" {
		return Reply{Spent: true}, errors.New("the model used its whole output budget before answering (reasoning models need more): raise DEEPCODE_MAX_OUTPUT_TOKENS")
	}
	return r, err
}

// maxAnswer bounds one answer's text. A wiki page or a chat answer is some
// kilobytes; a server streaming past this is broken and would fill memory
// until the timeout.
const maxAnswer = 1 << 20

// Answer gathers a streamed answer's text, relayed to OnDelta as it comes.
type Answer struct {
	strings.Builder
	OnDelta func(string)
}

// Add appends s, and fails once the answer passes maxAnswer.
func (a *Answer) Add(s string) error {
	if s == "" {
		return nil
	}
	a.WriteString(s)
	if a.OnDelta != nil {
		a.OnDelta(s)
	}
	if a.Len() > maxAnswer {
		return errors.New("the model's answer passed 1 MB and was stopped")
	}
	return nil
}

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// StripThinking drops <think> blocks that some servers leave in the content.
func StripThinking(s string) string { return strings.TrimSpace(thinkRe.ReplaceAllString(s, "")) }

// ErrContext is a request the server refused as longer than the model's
// context. Prompt and Context are the request's tokens and the context's when
// the server gives them, else 0; Err is the refusal as the backend words it.
// A caller that fits requests to the context learns from it (Chat.Learn).
type ErrContext struct {
	Prompt, Context int
	Err             error
}

// Error is the refusal's words, or that the request was too long.
func (e *ErrContext) Error() string {
	if e.Err == nil {
		return "the request is longer than the model's context"
	}
	return e.Err.Error()
}

// ErrUnavailable: the model could not be reached, or failed for now, unless it
// said the request was longer than the context. A later try may get through:
// a job waits and tries again.
var ErrUnavailable = errors.New("the model server is unavailable")

// ErrRefused: the model's server refused the request for a setting that is
// wrong (a key, a model, a path, an account out of credit, a login), which no
// retry fixes.
var ErrRefused = errors.New("the model server refused the request")

// ErrFailed: the model or its server failed the answer it was giving, or
// refused to give one, and sent none of its text: not a feature the server
// lacks. A structured output's turn asks again (package contract).
var ErrFailed = errors.New("the server failed the answer")

// ErrSpent: a reply cut at the output budget, whose calls may be cut too.
var ErrSpent = errors.New("the model's reply reached the output budget: raise DEEPCODE_MAX_OUTPUT_TOKENS")

// Kinded is err, which is also kind (errors.Is), its message its own.
func Kinded(err, kind error) error { return kinded{err, kind} }

// kinded is Kinded's error; package git has its twin, as neither imports the
// other.
type kinded struct {
	error
	kind error
}

// Is reports whether target is k's kind.
func (k kinded) Is(target error) bool { return target == k.kind }

// Unwrap is the error k carries.
func (k kinded) Unwrap() error { return k.error }

// WithTimeout bounds one request to the model name by d, unless d is not
// positive. Its cause names the model and the setting, which a bare "context
// deadline exceeded" in the log or in a job's error would not; Cause hands it
// on.
func WithTimeout(ctx context.Context, d time.Duration, name string) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, d,
		fmt.Errorf("no whole answer from %s within DEEPCODE_LLM_TIMEOUT (%s): %w", name, d, context.DeadlineExceeded))
}

// Cause is err, or once ctx has ended, why it ended: the request's own
// timeout, or the caller's reason (a job's timeout, a reader who left).
func Cause(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}
