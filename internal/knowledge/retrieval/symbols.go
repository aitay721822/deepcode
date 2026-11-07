package retrieval

import (
	"context"
	"strings"

	"deepcode/internal/knowledge/retrieval/store"
)

type SymbolHit = store.SymbolHit // a symbol the search behind the UI found

// Symbols is the symbol search behind the UI: substring match on names,
// exact matches first, then prefixes, then shorter names.
func (r *Retriever) Symbols(ctx context.Context, snapshotID int64, q string, limit int) ([]SymbolHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []SymbolHit{}, nil
	}
	return r.Store.Symbols(ctx, snapshotID, q, limit)
}

// Users returns chunks of the files that import a file defining one of names
// and use that name: the code that calls, handles or builds it, which a search
// for the name finds only when its words win. Each file gives one chunk, the
// files using most of names first, tests, docs and generated files aside; at
// most limit. Names match exactly, case and all, as the code holds them.
func (r *Retriever) Users(ctx context.Context, snapshotID int64, names []string, limit int) ([]Hit, error) {
	if len(names) == 0 {
		return nil, nil
	}
	ids, err := r.Store.Users(ctx, snapshotID, names, limit)
	if err != nil {
		return nil, err
	}
	return r.Store.Chunks(ctx, ids)
}
