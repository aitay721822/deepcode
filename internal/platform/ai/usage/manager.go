package usage

import (
	"context"
	"log"

	"deepcode/internal/platform/ai/usage/store"
)

// Total is one repository's spend on one model.
type Total = store.Total

// Manager is the Meter that keeps every request's tokens, summed per
// repository, day and model, and reads the sums back for the admin console.
type Manager struct{ Store *store.Store }

// Record counts u's tokens for the repository ctx names (0 when none) and the
// model. A request the server counted no tokens for, such as one refused, is
// left out. Counting outlives ctx, as the tokens were spent however the
// request ended, and its failure is logged, never the request's.
func (m *Manager) Record(ctx context.Context, model string, u Usage) {
	if u.InTokens == 0 && u.OutTokens == 0 && u.CachedTokens == 0 {
		return
	}
	row := store.Row{Repository: Repository(ctx), Model: model, In: u.InTokens, Cached: u.CachedTokens, Out: u.OutTokens}
	if err := m.Store.Add(context.WithoutCancel(ctx), row); err != nil {
		log.Printf("usage: counting %d tokens of %s for repository %d: %v", u.InTokens+u.OutTokens, model, row.Repository, err)
	}
}

// Totals is each repository's spend on each model over the last days days,
// today among them, or over all of them when days is 0, those keep says the
// viewer may see; the largest input first.
func (m *Manager) Totals(ctx context.Context, days int, keep func(repoID int64) bool) ([]Total, error) {
	all, err := m.Store.Totals(ctx, days)
	if err != nil {
		return nil, err
	}
	shown := all[:0]
	for _, t := range all {
		if keep(t.RepoID) {
			shown = append(shown, t)
		}
	}
	return shown, nil
}
