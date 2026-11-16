package compat

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/usage"
)

// Embed returns one vector per text, in input order.
func (c *Client) Embed(ctx context.Context, texts []string) (_ [][]float32, err error) {
	ctx, cancel := ai.WithTimeout(ctx, c.Timeout, c.ModelName)
	defer cancel()
	defer func() { err = ai.Cause(ctx, err) }() // before cancel, which would end ctx too
	var used ai.Usage
	if c.Meter != nil {
		start := time.Now()
		defer func() {
			used.Elapsed = time.Since(start)
			c.Meter.Record(ctx, c.ModelName, used)
		}()
	}
	var raw *http.Response
	svc := openai.NewEmbeddingService(c.options()...)
	res, err := svc.New(ctx, openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(c.ModelName),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	}, option.WithResponseInto(&raw))
	if err != nil {
		return nil, c.fail("/embeddings", raw, err)
	}
	used = usage.Embedding(res.Usage)
	vecs := make([][]float32, len(texts))
	for _, d := range res.Data {
		if d.Index < 0 || d.Index >= int64(len(vecs)) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		v := make([]float32, len(d.Embedding))
		for i, x := range d.Embedding {
			v[i] = float32(x)
		}
		vecs[d.Index] = v
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("no embedding returned for input %d", i)
		}
	}
	return vecs, nil
}
