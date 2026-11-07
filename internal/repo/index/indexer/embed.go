package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"deepcode/internal/platform/jobs"
)

const (
	embedBatchSize = 32
	reuseBatch     = 1000 // chunk ids per copy of stored vectors, so no copy holds the write lock for long
)

// embedInput is the exact text embedded for a chunk; the path and symbol give
// the model context the code alone lacks.
func embedInput(path, symbol, content string) string {
	s := "File: " + path + "\n"
	if symbol != "" {
		s += "Symbol: " + symbol + "\n"
	}
	s += "\n" + content
	if len(s) > 8000 {
		s = strings.ToValidUTF8(s[:8000], "")
	}
	return s
}

// contentHash covers the exact embedding input, so equal hashes can share a vector.
func contentHash(path, symbol, content string) string {
	sum := sha256.Sum256([]byte(embedInput(path, symbol, content)))
	return hex.EncodeToString(sum[:])
}

// embed fills in vectors for the snapshot's chunks: first by copying vectors of
// identical content from any snapshot, then by embedding each remaining
// distinct content once.
func (ix *Indexer) embed(ctx context.Context, snap int64, progress jobs.Progress) error {
	model := ix.Embed.ModelName
	progress.Stage("embed", 0, 0)
	if err := ix.reuse(ctx, snap, model); err != nil {
		return err
	}
	pending, err := ix.Store.Pending(ctx, snap, model)
	if err != nil {
		return err
	}
	progress.Logf("embedding %d distinct chunks with %s; the rest reuse stored vectors", len(pending), model)
	for i := 0; i < len(pending); i += embedBatchSize {
		ids := pending[i:min(i+embedBatchSize, len(pending))]
		if err := ix.embedBatch(ctx, snap, model, ids); err != nil {
			return err
		}
		progress.Stage("embed", i+len(ids), len(pending))
	}
	return nil
}

// reuse copies vectors stored for identical content, from any snapshot, onto
// the snapshot's chunks, one range of reuseBatch chunk ids at a time.
func (ix *Indexer) reuse(ctx context.Context, snap int64, model string) error {
	lo, hi, ok, err := ix.Store.ChunkIDs(ctx, snap)
	if err != nil {
		return err
	}
	for from := lo; ok && from <= hi; from += reuseBatch {
		if err := ix.Store.Reuse(ctx, snap, model, from, from+reuseBatch); err != nil {
			return err
		}
	}
	return nil
}

// embedBatch embeds the given chunks and stores each vector on every chunk of
// the snapshot with the same content.
func (ix *Indexer) embedBatch(ctx context.Context, snap int64, model string, ids []int64) error {
	inputs, err := ix.Store.Inputs(ctx, ids)
	if err != nil {
		return err
	}
	texts := make([]string, len(inputs))
	hashes := make([]string, len(inputs))
	for j, in := range inputs {
		texts[j], hashes[j] = embedInput(in.Path, in.Symbol, in.Content), in.Hash
	}
	vecs, err := ix.Embed.Embed(ctx, texts)
	if err != nil {
		return err
	}
	byHash := map[string][]float32{}
	for j, h := range hashes {
		byHash[h] = vecs[j]
	}
	chunks, err := ix.Store.WithHashes(ctx, snap, hashes)
	if err != nil {
		return err
	}
	vectors := map[int64][]float32{}
	for id, hash := range chunks {
		vectors[id] = byHash[hash]
	}
	return ix.Vectors.Upsert(ctx, model, vectors)
}
