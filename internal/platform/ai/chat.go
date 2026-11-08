package ai

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/shared"
)

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// maxAnswer bounds one answer's text. A wiki page or a chat answer is some
// kilobytes; a server streaming past this is broken and would fill memory
// until the timeout.
const maxAnswer = 1 << 20

// StripThinking drops <think> blocks that some servers leave in the content.
func StripThinking(s string) string { return strings.TrimSpace(thinkRe.ReplaceAllString(s, "")) }

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
func (c *OpenAI) Stream(ctx context.Context, messages []Message, onDelta func(string)) (_ string, err error) {
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
	spent, err := stream(ctx, messages, out, &usage)
	if err != nil {
		return out.String(), err
	}
	if spent && strings.TrimSpace(out.String()) == "" {
		return "", errors.New("the model used its whole output budget before answering (reasoning models need more): raise DEEPCODE_MAX_OUTPUT_TOKENS")
	}
	return out.String(), nil
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
// once the server says it ended, with [DONE] or a finish reason: a stream that
// just stops was cut short.
func (c *OpenAI) completions(ctx context.Context, messages []Message, out *answer, usage *Usage) (spent bool, err error) {
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
	// New on the raw body, not NewStreaming: its stream ends as quietly on
	// [DONE] as on a cut, which ended tells apart.
	var raw *http.Response
	svc := openai.NewChatCompletionService(c.options()...)
	if _, err := svc.New(ctx, params, option.WithJSONSet("stream", true), option.WithResponseBodyInto(&raw)); err != nil {
		return false, c.fail("/chat/completions", raw, err)
	}
	dec := &ended{Decoder: ssestream.NewDecoder(raw)}
	stream := ssestream.NewStream[openai.ChatCompletionChunk](dec, nil)
	defer stream.Close()
	finish := ""
	for stream.Next() {
		chunk := stream.Current()
		if u := chunk.Usage; chunk.JSON.Usage.Valid() {
			hit, _ := strconv.Atoi(u.JSON.ExtraFields["prompt_cache_hit_tokens"].Raw()) // DeepSeek
			usage.InTokens, usage.OutTokens = int(u.PromptTokens), int(u.CompletionTokens)
			usage.CachedTokens = max(hit, int(u.PromptTokensDetails.CachedTokens)) // OpenAI, llama.cpp
		}
		for _, ch := range chunk.Choices {
			if err := out.add(ch.Delta.Content); err != nil {
				return false, err
			}
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
		}
	}
	if err := stream.Err(); err != nil {
		return false, c.fail("/chat/completions", nil, err)
	}
	if !dec.done && finish == "" {
		return false, errors.New("the model's answer ended before it was finished")
	}
	return finish == "length", nil
}

// ended is a stream's decoder noting the [DONE] that ends it.
type ended struct {
	ssestream.Decoder
	done bool
}

func (d *ended) Next() bool {
	ok := d.Decoder.Next()
	d.done = d.done || ok && bytes.HasPrefix(d.Event().Data, []byte("[DONE]"))
	return ok
}
