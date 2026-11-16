package usage

import (
	"context"
	"log"

	"deepcode/internal/platform/ai/usage/store"
)

// Total is one repository's spend on one model.
type Total = store.Total

// Manager is the Meter that keeps every request, summed per repository, day
// and model, and reads the sums back for the admin console.
type Manager struct{ Store *store.Store }

// Record counts a request of the model for the repository ctx names (0 when
// none): its tokens, or, when the server counted none (a subscription agent's
// answer, a refused request), an unreported call, with the tokens estimated
// for an answer; and its time. Counting outlives ctx, as the request was made
// however it ended, and its failure is logged, never the request's.
func (m *Manager) Record(ctx context.Context, model string, u Usage) {
	row := store.Row{Repository: Repository(ctx), Model: model, In: u.InTokens, Cached: u.CachedTokens, Out: u.OutTokens,
		Unreported: !u.Reported(), EstIn: u.EstIn, EstOut: u.EstOut, Elapsed: u.Elapsed}
	if err := m.Store.Add(context.WithoutCancel(ctx), row); err != nil {
		log.Printf("usage: counting a request of %s for repository %d: %v", model, row.Repository, err)
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
