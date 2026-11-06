package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Embed returns one vector per text, in input order.
func (c *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.post(ctx, "/embeddings", map[string]any{"model": c.ModelName, "input": texts})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	// A batch's vectors are some megabytes; a server sending far more is broken.
	body := &io.LimitedReader{R: resp.Body, N: 64 << 20}
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		if body.N == 0 {
			return nil, errors.New("the embeddings answer passed 64 MB")
		}
		return nil, fmt.Errorf("decode embeddings: %w", err)
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("no embedding returned for input %d", i)
		}
	}
	return vecs, nil
}
