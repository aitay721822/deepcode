package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"deepcode/internal/repo/index/vectorstore"
)

// vector embeds the query (cached per model and query text) and returns the
// snapshot's nearest chunks.
func (r *Retriever) vector(ctx context.Context, snapshotID int64, query string) ([]int64, error) {
	model := r.Embed.ModelName
	b, err := r.Cache.GetOrLoad(ctx, "qvec:"+model+":"+digest(query), time.Hour, func() ([]byte, error) {
		v, err := r.Embed.Embed(context.WithoutCancel(ctx), []string{query}) // shared with the callers waiting for it
		if err != nil {
			return nil, err
		}
		return vectorstore.Encode(v[0]), nil
	})
	if err != nil {
		return nil, err
	}
	hits, err := r.Vectors.Search(ctx, snapshotID, model, vectorstore.Decode(b), vectorLimit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.ChunkID
	}
	return ids, nil
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
