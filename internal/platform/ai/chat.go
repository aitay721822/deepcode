package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"deepcode/internal/platform/ai/usage"
)

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// maxAnswer bounds one answer's text. A wiki page or a chat answer is some
// kilobytes; a server streaming past this is broken and would fill memory
// until the timeout.
const maxAnswer = 1 << 20

// StripThinking drops <think> blocks that some servers leave in the content.
func StripThinking(s string) string { return strings.TrimSpace(thinkRe.ReplaceAllString(s, "")) }

// Tool is a function the model may call (Exchange), with its arguments in a
// Format's JSON: the function bears the Format's name.
type Tool struct {
	Description string
	schema
}

// Tool is the function f.Name, called with f's JSON, which Decode reads.
func (f Format[T]) Tool(description string) Tool { return Tool{description, f.schema} }

// ToolCall is a call the model made: its id, the function's name and its
// arguments as the model wrote them, and its Key, the calls before it in its
// Exchange: unique where an id may not be.
type ToolCall struct {
	ID, Name, Arguments string
	Key                 int
}

// Generate returns the model's whole answer to p.
func (c *OpenAI) Generate(ctx context.Context, p Prompt) (string, error) {
	return c.Stream(ctx, p, nil)
}

// Usage is one answer's size, as package usage reads it from each API, and
// Meter what OpenAI.Meter tells it to.
type (
	Usage = usage.Usage
	Meter = usage.Meter
)

// Stream streams the model's answer to p, by the Responses API when API says
// so and by Chat Completions otherwise, and relays its text as it comes.
// Reasoning is no part of the answer.
func (c *OpenAI) Stream(ctx context.Context, p Prompt, onDelta func(string)) (string, error) {
	out, _, err := c.stream(ctx, p.messages(), nil, onDelta)
	return out, err
}

// stream is Stream asking for s's JSON unless s is nil, and saying too whether
// the answer stopped at the output budget.
func (c *OpenAI) stream(ctx context.Context, messages []Message, s *schema, onDelta func(string)) (_ string, spent bool, err error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	defer func() { err = cause(ctx, err) }() // before cancel, which would end ctx too
	out := &answer{onDelta: onDelta}
	var used Usage
	if c.Meter != nil {
		for _, m := range messages {
			used.InChars += utf8.RuneCountInString(m.Content)
		}
		defer func() { used.OutChars = utf8.RuneCountInString(out.String()); c.Meter.Record(ctx, c.ModelName, used) }()
	}
	stream := c.completions
	if c.API == Responses {
		stream = c.responses
	}
	spent, err = stream(ctx, messages, s, out, &used)
	if err != nil {
		return out.String(), spent, err
	}
	if spent && strings.TrimSpace(out.String()) == "" {
		return "", spent, errors.New("the model used its whole output budget before answering (reasoning models need more): raise DEEPCODE_MAX_OUTPUT_TOKENS")
	}
	return out.String(), spent, nil
}

// answer gathers a streamed answer's text, relayed to onDelta as it comes.
type answer struct {
	strings.Builder
	onDelta func(string)
}

// add appends s, and fails once the answer passes maxAnswer.
func (a *answer) add(s string) error {
	if s == "" {
		return nil
	}
	a.WriteString(s)
	if a.onDelta != nil {
		a.onDelta(s)
	}
	if a.Len() > maxAnswer {
		return errors.New("the model's answer passed 1 MB and was stopped")
	}
	return nil
}

// chatParams are the Chat Completions params of messages, with c's settings.
func (c *OpenAI) chatParams(messages []Message) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{Model: shared.ChatModel(c.ModelName)}
	for _, m := range messages {
		switch m.Role {
		case "system":
			params.Messages = append(params.Messages, openai.SystemMessage(m.Content))
		case "assistant":
			params.Messages = append(params.Messages, openai.AssistantMessage(m.Content))
		default:
			params.Messages = append(params.Messages, openai.UserMessage(m.Content))
		}
	}
	if c.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(c.MaxTokens))
	}
	if c.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(c.ReasoningEffort)
	}
	if c.Temperature != nil {
		params.Temperature = openai.Float(*c.Temperature)
	}
	return params
}

// completions streams a Chat Completions answer (/chat/completions) into out,
// adds its tokens to used, and says whether it stopped at the output budget.
// An answer is whole only once the server gave its finish reason: a stream
// that ends without one, even with [DONE], was cut short.
func (c *OpenAI) completions(ctx context.Context, messages []Message, s *schema, out *answer, used *Usage) (spent bool, err error) {
	params := c.chatParams(messages)
	if c.Meter != nil {
		params.StreamOptions.IncludeUsage = openai.Bool(true)
	}
	if s != nil {
		params.ResponseFormat.OfJSONSchema = &shared.ResponseFormatJSONSchemaParam{JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
			Name: s.Name, Schema: s.JSON, Strict: openai.Bool(true)}}
	}
	var res *http.Response
	svc := openai.NewChatCompletionService(c.options()...)
	stream := svc.NewStreaming(ctx, params, option.WithResponseInto(&res))
	defer stream.Close()
	var acc openai.ChatCompletionAccumulator
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		// Summed as the accumulator sums it, with the fields it does not know. A
		// stream cut before its usage chunk counts no tokens: the server never said.
		used.Add(usage.Chat(chunk.Usage))
		for _, ch := range chunk.Choices {
			if err := out.add(ch.Delta.Content); err != nil {
				return false, err
			}
		}
	}
	if err := stream.Err(); err != nil {
		return false, c.fail("/chat/completions", res, err)
	}
	if len(acc.Choices) == 0 || acc.Choices[0].FinishReason == "" {
		return false, errors.New("the model's answer ended before it was finished")
	}
	return acc.Choices[0].FinishReason == "length", nil
}

// errSpent: a reply cut at the output budget, whose calls may be cut too.
var errSpent = errors.New("the model's reply reached the output budget: raise DEEPCODE_MAX_OUTPUT_TOKENS")

// Exchange is a conversation in which the model may call tools, held in the
// SDK's params of the API its client asks by and continued as the SDK's
// README continues one: each reply is appended as the model gave it, then
// each call's result. Replies come whole, not streamed. It keeps the text the
// next request sends, piece by piece, for its caller to count in the model's
// tokens (Size); a call is known by its Key, as two calls may share an id.
type Exchange struct {
	c       *OpenAI
	chat    openai.ChatCompletionNewParams
	resp    responses.ResponseNewParams
	pieces  []string // the messages, the tools' definitions, each reply's text and calls, each result
	sizes   []int    // each piece as Size counted it, -1 until it does
	answers map[int]answered
	calls   int   // the calls so far, which number the next one's Key
	sent    int   // the pieces the last request sent
	used    Usage // the last reply's
}

// answered is where a call's result stands: its piece, and its message or
// input item in the params.
type answered struct{ piece, at int }

// Size is the next request's text in count's units, each piece counted once
// (count must not change), and the part of it the last request sent.
func (e *Exchange) Size(count func(string) int) (next, sent int) {
	for i, p := range e.pieces {
		if e.sizes[i] < 0 {
			e.sizes[i] = count(p)
		}
		if next += e.sizes[i]; i < e.sent {
			sent += e.sizes[i]
		}
	}
	return next, sent
}

// Added is the pieces the last request answered did not send: its reply's
// and the results'; all of them before the first reply.
func (e *Exchange) Added() int { return len(e.pieces) - e.sent }

// Used is the last reply's Usage: InTokens as the server counted them, 0 when
// it reported none.
func (e *Exchange) Used() Usage { return e.used }

// add keeps s as the next piece, and says which.
func (e *Exchange) add(s string) int {
	e.pieces, e.sizes = append(e.pieces, s), append(e.sizes, -1)
	return len(e.pieces) - 1
}

// chars is the pieces' characters.
func (e *Exchange) chars() (n int) {
	for _, p := range e.pieces {
		n += utf8.RuneCountInString(p)
	}
	return n
}

// Compact gives the calls short, by Key, the text short gives them in place
// of their results, in the params as they stand: the replies and the other
// results stay as they were, and so does a result no longer than its text.
// It returns how many it replaced.
func (e *Exchange) Compact(short map[int]string) int {
	n := 0
	for key, text := range short {
		a, ok := e.answers[key]
		if !ok || len(text) >= len(e.pieces[a.piece]) {
			continue
		}
		e.pieces[a.piece], e.sizes[a.piece], n = text, -1, n+1
		if e.c.API == Responses {
			id := e.resp.Input.OfInputItemList[a.at].OfFunctionCallOutput.CallID
			e.resp.Input.OfInputItemList[a.at] = responses.ResponseInputItemParamOfFunctionCallOutput(text)
			e.resp.Input.OfInputItemList[a.at].OfFunctionCallOutput.CallID = id
			continue
		}
		e.chat.Messages[a.at] = openai.ToolMessage(text, e.chat.Messages[a.at].OfTool.ToolCallID)
	}
	return n
}

// Exchange starts a conversation from p in which the model may call tools,
// its replies and their results following p's messages. The arguments are
// asked for strict, as structured outputs are.
func (c *OpenAI) Exchange(p Prompt, tools []Tool) *Exchange {
	e, messages := &Exchange{c: c, answers: map[int]answered{}}, p.messages()
	for _, m := range messages {
		e.add(m.Content)
	}
	for _, t := range tools {
		schema, _ := json.Marshal(t.JSON) // a map from JSON, so it marshals again
		e.add(t.Name + "\n" + t.Description + "\n" + string(schema))
	}
	if c.API == Responses {
		e.resp = c.responsesParams(messages)
		for _, t := range tools {
			e.resp.Tools = append(e.resp.Tools, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
				Name: t.Name, Description: openai.String(t.Description), Parameters: t.JSON, Strict: openai.Bool(true)}})
		}
		return e
	}
	e.chat = c.chatParams(messages)
	for _, t := range tools {
		e.chat.Tools = append(e.chat.Tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name: t.Name, Description: openai.String(t.Description), Parameters: t.JSON, Strict: openai.Bool(true)}))
	}
	return e
}

// Next asks for the model's next reply: its text and the tools it calls,
// each of which Answer answers before the next reply is asked for.
func (e *Exchange) Next(ctx context.Context) (text string, calls []ToolCall, err error) {
	ctx, cancel := e.c.withTimeout(ctx)
	defer cancel()
	defer func() { err = cause(ctx, err) }()
	sent, used := len(e.pieces), Usage{InChars: e.chars()}
	defer func() {
		if err == nil { // a refused request sent nothing the next one will not
			e.sent = sent
			if text != "" {
				e.add(text) // the reply is sent back with every request after it
			}
			for i := range calls {
				calls[i].Key, e.calls = e.calls, e.calls+1
				e.add(calls[i].Name + calls[i].Arguments)
			}
		}
		used.OutChars = utf8.RuneCountInString(text)
		for _, call := range calls {
			used.OutChars += utf8.RuneCountInString(call.Arguments)
		}
		if e.used = used; e.c.Meter != nil {
			e.c.Meter.Record(ctx, e.c.ModelName, used)
		}
	}()
	if e.c.API == Responses {
		return e.c.respond(ctx, &e.resp, &used)
	}
	return e.c.complete(ctx, &e.chat, &used)
}

// Answer gives the model result as what call returned.
func (e *Exchange) Answer(call ToolCall, result string) {
	a := answered{piece: e.add(result)}
	if e.c.API == Responses {
		out := responses.ResponseInputItemParamOfFunctionCallOutput(result)
		out.OfFunctionCallOutput.CallID = openai.String(call.ID)
		a.at, e.resp.Input.OfInputItemList = len(e.resp.Input.OfInputItemList), append(e.resp.Input.OfInputItemList, out)
	} else {
		a.at, e.chat.Messages = len(e.chat.Messages), append(e.chat.Messages, openai.ToolMessage(result, call.ID))
	}
	e.answers[call.Key] = a
}

// complete asks Chat Completions (/chat/completions) for the next reply of
// params, appends it to them, and adds its tokens to used.
func (c *OpenAI) complete(ctx context.Context, params *openai.ChatCompletionNewParams, used *Usage) (string, []ToolCall, error) {
	var res *http.Response
	svc := openai.NewChatCompletionService(c.options()...)
	completion, err := svc.New(ctx, *params, option.WithResponseInto(&res))
	if err != nil {
		return "", nil, c.fail("/chat/completions", res, err)
	}
	used.Add(usage.Chat(completion.Usage))
	switch {
	case len(completion.Choices) == 0:
		return "", nil, errors.New("the model sent no reply")
	case completion.Choices[0].FinishReason == "length":
		return "", nil, errSpent
	}
	msg := completion.Choices[0].Message
	params.Messages = append(params.Messages, msg.ToParam())
	var calls []ToolCall
	for _, tc := range msg.ToolCalls {
		calls = append(calls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return msg.Content, calls, nil
}
