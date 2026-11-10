package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	checker "github.com/google/jsonschema-go/jsonschema"
	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// maxAnswer bounds one answer's text. A wiki page or a chat answer is some
// kilobytes; a server streaming past this is broken and would fill memory
// until the timeout.
const maxAnswer = 1 << 20

// StripThinking drops <think> blocks that some servers leave in the content.
func StripThinking(s string) string { return strings.TrimSpace(thinkRe.ReplaceAllString(s, "")) }

// ErrSchema: an answer asked for in a JSON schema (structured outputs) that is
// not that JSON. A server that refuses the request fails with its own words,
// as for any other request.
var ErrSchema = errors.New("the chat endpoint must support structured outputs (json_schema)")

// Format is a structured output: answers asked for in the JSON schema of a T
// and read into a T.
type Format[T any] struct{ schema }

// schema is a JSON schema, under the name a request gives it, and resolved
// to check answers by.
type schema struct {
	Name  string
	JSON  map[string]any
	check *checker.Resolved
}

// Schema is the Format of T, its schema made as the SDK's README makes one
// (GenerateSchema): every field required, no other, no $ref.
func Schema[T any](name string) Format[T] {
	r := jsonschema.Reflector{AllowAdditionalProperties: false, DoNotReference: true}
	var v T
	data, err := json.Marshal(r.Reflect(v))
	if err != nil {
		panic(err) // a Go type no schema describes: a mistake in the code
	}
	// Kept as raw JSON, so no number is rounded through float64.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		panic(err)
	}
	var c checker.Schema
	if err := json.Unmarshal(data, &c); err != nil {
		panic(err)
	}
	s := schema{Name: name, JSON: make(map[string]any, len(raw))}
	if s.check, err = c.Resolve(nil); err != nil {
		panic(err)
	}
	for k, v := range raw {
		s.JSON[k] = v
	}
	return Format[T]{s}
}

// Tool is a function the model may call (Exchange), with its arguments in a
// Format's JSON: the function bears the Format's name.
type Tool struct {
	Description string
	schema
}

// Tool is the function f.Name, called with f's JSON, which Decode reads.
func (f Format[T]) Tool(description string) Tool { return Tool{description, f.schema} }

// ToolCall is a call the model made: its id, the function's name and its
// arguments as the model wrote them.
type ToolCall struct{ ID, Name, Arguments string }

// Decode reads text, f's JSON, into a T, its schema checked, so {} is not a T
// with nothing in it.
func Decode[T any](f Format[T], text string) (v T, err error) {
	var doc any
	if err = json.Unmarshal([]byte(text), &doc); err == nil {
		err = f.check.Validate(doc)
	}
	if err == nil {
		err = json.Unmarshal([]byte(text), &v)
	}
	if err != nil {
		return v, fmt.Errorf("not the JSON of %s (%w)", f.Name, err)
	}
	return v, nil
}

// StreamJSON streams c's answer to p in f's JSON, relaying its text as it
// comes, and reads it into a T (Decode); reply is the answer as written. An
// answer that is not f's JSON is an error wrapping ErrSchema; one cut at the
// output budget says so instead.
func StreamJSON[T any](ctx context.Context, c *OpenAI, p Prompt, f Format[T], onDelta func(string)) (v T, reply string, err error) {
	reply, spent, err := c.stream(ctx, p.messages(), &f.schema, onDelta)
	if err != nil {
		return v, reply, err
	}
	if spent && !json.Valid([]byte(reply)) {
		return v, reply, fmt.Errorf("%s: the answer reached the output budget before its JSON ended: raise DEEPCODE_MAX_OUTPUT_TOKENS", c.ModelName)
	}
	if v, err = Decode(f, reply); err != nil {
		return v, reply, fmt.Errorf("%s: the answer is %w: %w", c.ModelName, err, ErrSchema)
	}
	return v, reply, nil
}

// Generate returns the model's whole answer to p.
func (c *OpenAI) Generate(ctx context.Context, p Prompt) (string, error) {
	return c.Stream(ctx, p, nil)
}

// Usage is one answer's size: the characters sent and received, and the
// tokens the server counted, 0 when it reports none. CachedTokens are the
// input tokens the server read from its prompt cache: the prefix it had seen.
type Usage struct {
	InChars, OutChars, InTokens, OutTokens, CachedTokens int
}

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
	var usage Usage
	if c.Measure != nil {
		for _, m := range messages {
			usage.InChars += utf8.RuneCountInString(m.Content)
		}
		defer func() { usage.OutChars = utf8.RuneCountInString(out.String()); c.Measure(usage) }()
	}
	stream := c.completions
	if c.API == Responses {
		stream = c.responses
	}
	spent, err = stream(ctx, messages, s, out, &usage)
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

// deepSeekHits are the cached input tokens DeepSeek counts
// (prompt_cache_hit_tokens), where OpenAI and llama.cpp count cached_tokens.
func deepSeekHits(u openai.CompletionUsage) int {
	n, _ := strconv.Atoi(u.JSON.ExtraFields["prompt_cache_hit_tokens"].Raw())
	return n
}

// completions streams a Chat Completions answer (/chat/completions) into out,
// and says whether it stopped at the output budget. An answer is whole only
// once the server gave its finish reason: a stream that ends without one, even
// with [DONE], was cut short.
func (c *OpenAI) completions(ctx context.Context, messages []Message, s *schema, out *answer, usage *Usage) (spent bool, err error) {
	params := c.chatParams(messages)
	if c.Measure != nil {
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
	hit := 0 // DeepSeek's cached tokens, which the accumulator does not sum
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		hit = max(hit, deepSeekHits(chunk.Usage))
		for _, ch := range chunk.Choices {
			if err := out.add(ch.Delta.Content); err != nil {
				return false, err
			}
		}
	}
	u := acc.Usage
	usage.InTokens, usage.OutTokens = int(u.PromptTokens), int(u.CompletionTokens)
	usage.CachedTokens = max(hit, int(u.PromptTokensDetails.CachedTokens)) // OpenAI's, llama.cpp's
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
// each call's result. Replies come whole, not streamed.
type Exchange struct {
	c     *OpenAI
	chat  openai.ChatCompletionNewParams
	resp  responses.ResponseNewParams
	chars int // characters of the messages, replies and results so far, each request's for Measure
}

// Exchange starts a conversation from p in which the model may call tools,
// its replies and their results following p's messages. The arguments are
// asked for strict, as structured outputs are.
func (c *OpenAI) Exchange(p Prompt, tools []Tool) *Exchange {
	e, messages := &Exchange{c: c}, p.messages()
	for _, m := range messages {
		e.chars += utf8.RuneCountInString(m.Content)
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
	usage := Usage{InChars: e.chars}
	defer func() {
		usage.OutChars = utf8.RuneCountInString(text)
		for _, call := range calls {
			usage.OutChars += utf8.RuneCountInString(call.Arguments)
		}
		e.chars += usage.OutChars // the reply is sent back with every request after it
		if e.c.Measure != nil {
			e.c.Measure(usage)
		}
	}()
	if e.c.API == Responses {
		return e.c.respond(ctx, &e.resp, &usage)
	}
	return e.c.complete(ctx, &e.chat, &usage)
}

// Answer gives the model result as what call returned.
func (e *Exchange) Answer(call ToolCall, result string) {
	e.chars += utf8.RuneCountInString(result)
	if e.c.API == Responses {
		out := responses.ResponseInputItemParamOfFunctionCallOutput(result)
		out.OfFunctionCallOutput.CallID = openai.String(call.ID)
		e.resp.Input.OfInputItemList = append(e.resp.Input.OfInputItemList, out)
		return
	}
	e.chat.Messages = append(e.chat.Messages, openai.ToolMessage(result, call.ID))
}

// complete asks Chat Completions (/chat/completions) for the next reply of
// params, and appends it to them.
func (c *OpenAI) complete(ctx context.Context, params *openai.ChatCompletionNewParams, usage *Usage) (string, []ToolCall, error) {
	var res *http.Response
	svc := openai.NewChatCompletionService(c.options()...)
	completion, err := svc.New(ctx, *params, option.WithResponseInto(&res))
	if err != nil {
		return "", nil, c.fail("/chat/completions", res, err)
	}
	u := completion.Usage
	usage.InTokens, usage.OutTokens = int(u.PromptTokens), int(u.CompletionTokens)
	usage.CachedTokens = max(deepSeekHits(u), int(u.PromptTokensDetails.CachedTokens))
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
