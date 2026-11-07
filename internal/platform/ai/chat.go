package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
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
// tokens the server counted, 0 when it reports none.
type Usage struct {
	InChars, OutChars, InTokens, OutTokens int
}

// Stream sends a streaming chat completion and relays content fragments.
// Reasoning fragments (reasoning_content) are not part of the answer and are
// ignored. An answer is whole only once the server says it ended, with [DONE]
// or a finish reason: a stream that just stops was cut short.
func (c *OpenAI) Stream(ctx context.Context, messages []Message, onDelta func(string)) (_ string, err error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	defer func() { err = cause(ctx, err) }() // before cancel, which would end ctx too
	body := map[string]any{"model": c.ModelName, "messages": messages, "stream": true}
	if c.MaxTokens > 0 {
		body["max_tokens"] = c.MaxTokens
	}
	if c.ReasoningEffort != "" {
		body["reasoning_effort"] = c.ReasoningEffort
	}
	var out strings.Builder
	var usage Usage
	if c.Measure != nil {
		body["stream_options"] = map[string]any{"include_usage": true}
		for _, m := range messages {
			usage.InChars += utf8.RuneCountInString(m.Content)
		}
		defer func() { usage.OutChars = utf8.RuneCountInString(out.String()); c.Measure(usage) }()
	}
	resp, err := c.post(ctx, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	finish, done := "", false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Usage != nil {
			usage.InTokens, usage.OutTokens = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
		}
		if chunk.Error != nil {
			return out.String(), errors.New(c.quote([]byte(chunk.Error.Message)))
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				out.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
		}
		if out.Len() > maxAnswer {
			return out.String(), errors.New("the model's answer passed 1 MB and was stopped")
		}
	}
	if err := sc.Err(); err != nil {
		return out.String(), err
	}
	if !done && finish == "" {
		return out.String(), errors.New("the model's answer ended before it was finished")
	}
	if finish == "length" && strings.TrimSpace(out.String()) == "" {
		return "", errors.New("the model used its whole output budget before answering (reasoning models need more): raise DEEPCODE_MAX_OUTPUT_TOKENS")
	}
	return out.String(), nil
}
