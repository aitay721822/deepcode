// Package usage is what model requests cost: the tokens each server counted,
// read from its API's own fields in one place (Chat, Responses, Embedding),
// told to a Meter with the request's context, and kept per repository by
// Manager for the admin console.
package usage

import (
	"context"
	"strconv"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

// Usage is one request's size: the characters sent and received, and the
// tokens the server counted, 0 when it reports none. CachedTokens are the
// part of InTokens the server read from its prompt cache: the prefix it had
// seen.
type Usage struct {
	InChars, OutChars, InTokens, OutTokens, CachedTokens int
}

// Add adds o to u, field by field.
func (u *Usage) Add(o Usage) {
	u.InChars, u.OutChars = u.InChars+o.InChars, u.OutChars+o.OutChars
	u.InTokens, u.OutTokens, u.CachedTokens = u.InTokens+o.InTokens, u.OutTokens+o.OutTokens, u.CachedTokens+o.CachedTokens
}

// Chat is the tokens of a Chat Completions usage, or of one streamed chunk's.
// OpenAI and llama.cpp count the cached input in prompt_tokens_details;
// DeepSeek counts it in prompt_cache_hit_tokens, a field the SDK keeps as an
// extra one. Both are part of prompt_tokens.
func Chat(u openai.CompletionUsage) Usage {
	hit, _ := strconv.Atoi(u.JSON.ExtraFields["prompt_cache_hit_tokens"].Raw())
	return Usage{InTokens: int(u.PromptTokens), OutTokens: int(u.CompletionTokens), CachedTokens: max(hit, int(u.PromptTokensDetails.CachedTokens))}
}

// Responses is the tokens of a Responses API usage.
func Responses(u responses.ResponseUsage) Usage {
	return Usage{InTokens: int(u.InputTokens), OutTokens: int(u.OutputTokens), CachedTokens: int(u.InputTokensDetails.CachedTokens)}
}

// Embedding is the tokens of an embeddings usage: input only.
func Embedding(u openai.CreateEmbeddingResponseUsage) Usage {
	return Usage{InTokens: int(u.PromptTokens)}
}

// Meter is told every model request's Usage, failed ones too, with the
// request's context, which says whose it was (Repository), and the model's
// name.
type Meter interface {
	Record(ctx context.Context, model string, u Usage)
}

// Func is a Meter of a function.
type Func func(ctx context.Context, model string, u Usage)

// Record calls f.
func (f Func) Record(ctx context.Context, model string, u Usage) { f(ctx, model, u) }

type repositoryKey struct{}

// WithRepository is ctx saying that the model requests made under it are for
// repository id (its row id).
func WithRepository(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, repositoryKey{}, id)
}

// Repository is the repository ctx's requests are for (WithRepository), or 0.
func Repository(ctx context.Context) int64 {
	id, _ := ctx.Value(repositoryKey{}).(int64)
	return id
}
