package compat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/usage"
)

// Ask streams the model's answer to p, by the Responses API when API says so
// and by Chat Completions otherwise, asking the server for s's JSON when s is
// not nil and the schema is the server's (SchemaMode), and relays its text as
// it comes. Reasoning is no part of the answer.
func (c *Client) Ask(ctx context.Context, p ai.Prompt, s *ai.JSONSchema, onDelta func(string)) (_ ai.Reply, err error) {
	if c.SchemaMode() == ai.SchemaPrompt {
		s = nil // the prompt holds it
	}
	ctx, cancel := ai.WithTimeout(ctx, c.Timeout, c.ModelName)
	defer cancel()
	defer func() { err = ai.Cause(ctx, err) }() // before cancel, which would end ctx too
	messages := p.Messages()
	out := &ai.Answer{OnDelta: onDelta}
	var used ai.Usage
	if c.Meter != nil {
		start, sent := time.Now(), make([]string, len(messages))
		for i, m := range messages {
			used.InChars, sent[i] = used.InChars+utf8.RuneCountInString(m.Content), m.Content
		}
		defer func() {
			used.OutChars, used.Elapsed = utf8.RuneCountInString(out.String()), time.Since(start)
			if err == nil { // a failed request's tokens are not counted: most were never spent
				used.Estimate(c.Count, sent, []string{out.String()})
			}
			c.Meter.Record(ctx, c.ModelName, used)
		}()
	}
	stream := c.completions
	if c.API == Responses {
		stream = c.responses
	}
	spent, err := stream(ctx, messages, s, out, &used)
	return ai.Reply{Text: out.String(), Spent: spent}, err
}

// chatParams are the Chat Completions params of messages, with c's settings.
func (c *Client) chatParams(messages []ai.Message) openai.ChatCompletionNewParams {
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
func (c *Client) completions(ctx context.Context, messages []ai.Message, s *ai.JSONSchema, out *ai.Answer, used *ai.Usage) (spent bool, err error) {
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
			if err := out.Add(ch.Delta.Content); err != nil {
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

// exchange is an ai.Exchange held in the SDK's params of the API its client
// asks by and continued as the SDK's README continues one: each reply is
// appended as the model gave it, then each call's result. Replies come whole,
// not streamed.
type exchange struct {
	ai.Pieces
	c       *Client
	chat    openai.ChatCompletionNewParams
	resp    responses.ResponseNewParams
	answers map[int]answered
	calls   int      // the calls so far, which number the next one's Key
	used    ai.Usage // the last reply's
}

// answered is where a call's result stands: its piece, and its message or
// input item in the params.
type answered struct{ piece, at int }

// Used is the last reply's Usage: InTokens as the server counted them, 0 when
// it reported none.
func (e *exchange) Used() ai.Usage { return e.used }

// Close does nothing: every request carries the whole exchange, and the
// server keeps none of it.
func (e *exchange) Close() {}

// Compact gives the calls short, by Key, the text short gives them in place
// of their results, in the params as they stand: the replies and the other
// results stay as they were, and so does a result no longer than its text.
// It returns how many it replaced.
func (e *exchange) Compact(short map[int]string) int {
	n := 0
	for key, text := range short {
		a, ok := e.answers[key]
		if !ok || len(text) >= len(e.Piece(a.piece)) {
			continue
		}
		e.Replace(a.piece, text)
		n++
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
func (c *Client) Exchange(p ai.Prompt, tools []ai.Tool) ai.Exchange {
	e, messages := &exchange{c: c, answers: map[int]answered{}}, p.Messages()
	for _, m := range messages {
		e.Add(m.Content)
	}
	for _, t := range tools {
		schema, _ := json.Marshal(t.JSON) // a map from JSON, so it marshals again
		e.Add(t.Name + "\n" + t.Description + "\n" + string(schema))
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
func (e *exchange) Next(ctx context.Context) (text string, calls []ai.ToolCall, err error) {
	ctx, cancel := ai.WithTimeout(ctx, e.c.Timeout, e.c.ModelName)
	defer cancel()
	defer func() { err = ai.Cause(ctx, err) }()
	start, sent, used := time.Now(), e.Len(), ai.Usage{InChars: e.Chars()}
	defer func() {
		if err == nil { // a refused request sent nothing the next one will not
			e.MarkSent(sent)
			if text != "" {
				e.Add(text) // the reply is sent back with every request after it
			}
			for i := range calls {
				calls[i].Key, e.calls = e.calls, e.calls+1
				e.Add(calls[i].Name + calls[i].Arguments)
			}
		}
		out := []string{text}
		for _, call := range calls {
			out = append(out, call.Arguments)
		}
		for _, s := range out {
			used.OutChars += utf8.RuneCountInString(s)
		}
		if used.Elapsed = time.Since(start); e.c.Meter != nil {
			if err == nil {
				used.Estimate(e.c.Count, e.Texts(sent), out)
			}
			e.c.Meter.Record(ctx, e.c.ModelName, used)
		}
		e.used = used
	}()
	if e.c.API == Responses {
		return e.c.respond(ctx, &e.resp, &used)
	}
	return e.c.complete(ctx, &e.chat, &used)
}

// Answer gives the model result as what call returned.
func (e *exchange) Answer(call ai.ToolCall, result string) {
	a := answered{piece: e.Add(result)}
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
func (c *Client) complete(ctx context.Context, params *openai.ChatCompletionNewParams, used *ai.Usage) (string, []ai.ToolCall, error) {
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
		return "", nil, ai.ErrSpent
	}
	msg := completion.Choices[0].Message
	params.Messages = append(params.Messages, msg.ToParam())
	var calls []ai.ToolCall
	for _, tc := range msg.ToolCalls {
		calls = append(calls, ai.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return msg.Content, calls, nil
}
