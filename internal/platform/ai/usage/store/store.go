// Package store keeps the model requests' counts in SQLite: llm_usage, one
// row per repository, UTC day and model.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store runs llm_usage's SQL.
type Store struct{ DB *sql.DB }

// Row is one request and whose it was: Repository is 0 when no repository
// asked for it. In includes Cached. Unreported is a request the server
// counted no tokens for; EstIn and EstOut are an answer's tokens as
// estimated then, Elapsed its time.
type Row struct {
	Repository      int64
	Model           string
	In, Cached, Out int
	Unreported      bool
	EstIn, EstOut   int
	Elapsed         time.Duration
}

// Total is the spend of one repository on one model. A repository deleted
// since keeps its spend and has no public id or name; RepoID 0 is the
// requests no repository asked for.
type Total struct {
	RepoID       int64  `json:"-"`
	RepoPublicID string `json:"repoId"`
	RepoOwner    string `json:"repoOwner"`
	RepoName     string `json:"repoName"`
	Model        string `json:"model"`
	Calls        int64  `json:"calls"`
	Input        int64  `json:"inputTokens"`
	Cached       int64  `json:"cachedTokens"`
	Output       int64  `json:"outputTokens"`
	Unreported   int64  `json:"unreportedCalls"` // the calls the server counted no tokens for
	EstInput     int64  `json:"estInputTokens"`  // the tokens of those answered, as a local tokenizer estimated them
	EstOutput    int64  `json:"estOutputTokens"`
	Milliseconds int64  `json:"milliseconds"` // every call's time, summed
}

// Add counts r on today's row of its repository and model.
func (s *Store) Add(ctx context.Context, r Row) error {
	unreported := 0
	if r.Unreported {
		unreported = 1
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO llm_usage(repository_id, day, model, calls, input_tokens, cached_tokens, output_tokens,
			unreported_calls, est_input_tokens, est_output_tokens, milliseconds)
		VALUES (?, date('now'), ?, 1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository_id, day, model) DO UPDATE SET calls = calls + 1, input_tokens = input_tokens + excluded.input_tokens,
			cached_tokens = cached_tokens + excluded.cached_tokens, output_tokens = output_tokens + excluded.output_tokens,
			unreported_calls = unreported_calls + excluded.unreported_calls, est_input_tokens = est_input_tokens + excluded.est_input_tokens,
			est_output_tokens = est_output_tokens + excluded.est_output_tokens, milliseconds = milliseconds + excluded.milliseconds`,
		r.Repository, r.Model, r.In, r.Cached, r.Out, unreported, r.EstIn, r.EstOut, r.Elapsed.Milliseconds())
	return err
}

// Totals sums each repository's spend on each model over the last days UTC
// days, today among them, or over all of them when days is 0; the largest
// input, counted and estimated, first.
func (s *Store) Totals(ctx context.Context, days int) ([]Total, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.repository_id, COALESCE(r.public_id, ''), COALESCE(r.owner, ''), COALESCE(r.name, ''),
			u.model, SUM(u.calls), SUM(u.input_tokens), SUM(u.cached_tokens), SUM(u.output_tokens),
			SUM(u.unreported_calls), SUM(u.est_input_tokens), SUM(u.est_output_tokens), SUM(u.milliseconds)
		FROM llm_usage u LEFT JOIN repositories r ON r.id = u.repository_id
		WHERE ? = 0 OR u.day >= date('now', ?)
		GROUP BY u.repository_id, u.model
		ORDER BY SUM(u.input_tokens + u.est_input_tokens) DESC, u.repository_id, u.model`, days, fmt.Sprintf("-%d days", days-1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := []Total{}
	for rows.Next() {
		var t Total
		if err := rows.Scan(&t.RepoID, &t.RepoPublicID, &t.RepoOwner, &t.RepoName, &t.Model, &t.Calls, &t.Input, &t.Cached, &t.Output,
			&t.Unreported, &t.EstInput, &t.EstOutput, &t.Milliseconds); err != nil {
			return nil, err
		}
		totals = append(totals, t)
	}
	return totals, rows.Err()
}
