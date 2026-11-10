package ai

import (
	"context"
	"errors"
	"net/http"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// The APIs a chat model is asked by (OpenAI.API).
const (
	Completions = "completions" // Chat Completions, /chat/completions: every server has it
	Responses   = "responses"   // the Responses API, /responses
)

// responses streams a Responses API answer (/responses) into out, and says
// whether it stopped at the output budget. The answer is whole once the
// response completed, or ended incomplete; one that failed is an error. The
// server is asked to store nothing, as no later request names it.
func (c *OpenAI) responses(ctx context.Context, messages []Message, s *schema, out *answer, usage *Usage) (spent bool, err error) {
	params := c.responsesParams(messages)
	if s != nil {
		params.Text.Format.OfJSONSchema = &responses.ResponseFormatTextJSONSchemaConfigParam{Name: s.Name, Schema: s.JSON, Strict: openai.Bool(true)}
	}
	var res *http.Response
	svc := responses.NewResponseService(c.options()...)
	stream := svc.NewStreaming(ctx, params, option.WithResponseInto(&res))
	defer stream.Close()
	for stream.Next() {
		ev := stream.Current()
		switch ev.Type {
		case "response.output_text.delta":
			if err := out.add(ev.Delta); err != nil {
				return false, err
			}
		case "response.completed", "response.incomplete":
			u := ev.Response.Usage
			usage.InTokens, usage.OutTokens, usage.CachedTokens = int(u.InputTokens), int(u.OutputTokens), int(u.InputTokensDetails.CachedTokens)
			return ev.Response.IncompleteDetails.Reason == "max_output_tokens", nil
		case "response.failed":
			return false, c.refused("/responses", ev.Response.Error.Message)
		case "error":
			return false, c.refused("/responses", ev.Message)
		}
	}
	if err := stream.Err(); err != nil {
		return false, c.fail("/responses", res, err)
	}
	return false, errors.New("the model's answer ended before it was finished")
}

// responsesParams are the Responses API params of messages, with c's
// settings, asking for nothing stored: no later request names a response, as
// each sends the whole conversation.
func (c *OpenAI) responsesParams(messages []Message) responses.ResponseNewParams {
	params := responses.ResponseNewParams{Model: shared.ResponsesModel(c.ModelName), Store: openai.Bool(false)}
	for _, m := range messages { // system, user and assistant are roles of both APIs
		params.Input.OfInputItemList = append(params.Input.OfInputItemList,
			responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRole(m.Role)))
	}
	if c.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(c.MaxTokens))
	}
	if c.ReasoningEffort != "" {
		params.Reasoning.Effort = shared.ReasoningEffort(c.ReasoningEffort)
	}
	if c.Temperature != nil {
		params.Temperature = openai.Float(*c.Temperature)
	}
	return params
}

// respond asks the Responses API (/responses) for the next reply of params,
// and appends it to them: its text as an assistant message, then its function
// calls, as nothing is stored to name instead.
// ponytail: a reasoning model's reasoning items are not sent back, so its
// reasoning starts again each reply; ask for reasoning.encrypted_content and
// send those items back if a hosted reasoning model ever gathers.
func (c *OpenAI) respond(ctx context.Context, params *responses.ResponseNewParams, usage *Usage) (string, []ToolCall, error) {
	var res *http.Response
	svc := responses.NewResponseService(c.options()...)
	r, err := svc.New(ctx, *params, option.WithResponseInto(&res))
	if err != nil {
		return "", nil, c.fail("/responses", res, err)
	}
	u := r.Usage
	usage.InTokens, usage.OutTokens, usage.CachedTokens = int(u.InputTokens), int(u.OutputTokens), int(u.InputTokensDetails.CachedTokens)
	switch {
	case r.Status == responses.ResponseStatusFailed:
		return "", nil, c.refused("/responses", r.Error.Message)
	case r.IncompleteDetails.Reason == "max_output_tokens":
		return "", nil, errSpent
	}
	text := r.OutputText()
	if text != "" {
		params.Input.OfInputItemList = append(params.Input.OfInputItemList,
			responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleAssistant))
	}
	var calls []ToolCall
	for _, item := range r.Output {
		if item.Type == "function_call" {
			call := item.AsFunctionCall()
			params.Input.OfInputItemList = append(params.Input.OfInputItemList,
				responses.ResponseInputItemParamOfFunctionCall(call.Arguments, call.CallID, call.Name))
			calls = append(calls, ToolCall{ID: call.CallID, Name: call.Name, Arguments: call.Arguments})
		}
	}
	return text, calls, nil
}
