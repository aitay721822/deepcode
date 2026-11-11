package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/indexer/vectorstore"
)

// secondaryCosine weighs the cosine of a test, example, doc, hidden-folder or
// generated chunk, as the keyword search quarters its BM25 (store.secondary):
// a test's or a guide's text sits as near a question as the code it exercises
// or explains, and express's trust-proxy question lost lib/utils.js to its
// test/req.*.js files. Not a quarter: cosines of related text lie close
// together, and 0.8 already moves these past the code that answers; 0.5
// scored the same on the eval, 0.8 keeping a little more recall.
const secondaryCosine = 0.8

// weigh is a chunk's cosine weight by its file (vectorstore.SQLite.Search).
func weigh(path string, generated bool) float32 {
	if generated || analyzer.Secondary(path) {
		return secondaryCosine
	}
	return 1
}

// vector embeds the query (cached per model and query text) and returns the
// snapshot's nearest chunks, tests and docs weighed down (weigh).
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
	hits, err := r.Vectors.Search(ctx, snapshotID, model, vectorstore.Decode(b), vectorLimit, weigh)
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
