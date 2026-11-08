package ai

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Embed returns one vector per text, in input order.
func (c *OpenAI) Embed(ctx context.Context, texts []string) (_ [][]float32, err error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	defer func() { err = cause(ctx, err) }() // before cancel, which would end ctx too
	var raw *http.Response
	svc := openai.NewEmbeddingService(c.options()...)
	res, err := svc.New(ctx, openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(c.ModelName),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	}, option.WithResponseInto(&raw))
	if err != nil {
		return nil, c.fail("/embeddings", raw, err)
	}
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
