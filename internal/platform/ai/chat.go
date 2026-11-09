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

// StreamJSON streams c's answer to messages in f's JSON, relaying its text as
// it comes, and reads it into a T; reply is the answer as written. An answer
// that is not f's JSON (its schema checked, so {} is not a T with nothing in
// it) is an error wrapping ErrSchema; one cut at the output budget says so
// instead.
func StreamJSON[T any](ctx context.Context, c *OpenAI, messages []Message, f Format[T], onDelta func(string)) (v T, reply string, err error) {
	reply, spent, err := c.stream(ctx, messages, &f.schema, onDelta)
	if err != nil {
		return v, reply, err
	}
	var doc any
	if err = json.Unmarshal([]byte(reply), &doc); err != nil && spent {
		return v, reply, fmt.Errorf("%s: the answer reached the output budget before its JSON ended: raise DEEPCODE_MAX_OUTPUT_TOKENS", c.ModelName)
	}
	if err == nil {
		err = f.check.Validate(doc)
	}
	if err == nil {
		err = json.Unmarshal([]byte(reply), &v)
	}
	if err != nil {
		return v, reply, fmt.Errorf("%s: the answer is not the JSON of %s (%v): %w", c.ModelName, f.Name, err, ErrSchema)
	}
	return v, reply, nil
}

// Generate returns the model's whole answer to messages.
func (c *OpenAI) Generate(ctx context.Context, messages []Message) (string, error) {
	return c.Stream(ctx, messages, nil)
}

// Usage is one answer's size: the characters sent and received, and the
// tokens the server counted, 0 when it reports none. CachedTokens are the
// input tokens the server read from its prompt cache: the prefix it had seen.
type Usage struct {
	InChars, OutChars, InTokens, OutTokens, CachedTokens int
}

// Stream streams the model's answer to messages, by the Responses API when
// API says so and by Chat Completions otherwise, and relays its text as it
// comes. Reasoning is no part of the answer.
func (c *OpenAI) Stream(ctx context.Context, messages []Message, onDelta func(string)) (string, error) {
	out, _, err := c.stream(ctx, messages, nil, onDelta)
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

// completions streams a Chat Completions answer (/chat/completions) into out,
// and says whether it stopped at the output budget. An answer is whole only
// once the server gave its finish reason: a stream that ends without one, even
// with [DONE], was cut short.
func (c *OpenAI) completions(ctx context.Context, messages []Message, s *schema, out *answer, usage *Usage) (spent bool, err error) {
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
		if n, err := strconv.Atoi(chunk.Usage.JSON.ExtraFields["prompt_cache_hit_tokens"].Raw()); err == nil {
			hit = n
		}
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
