// Package store keeps the model servers' token counts in SQLite: llm_usage,
// one row per repository, UTC day and model.
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Store runs llm_usage's SQL.
type Store struct{ DB *sql.DB }

// Row is one request's tokens and whose they were: Repository is 0 when no
// repository asked for it. In includes Cached.
type Row struct {
	Repository      int64
	Model           string
	In, Cached, Out int
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
}

// Add counts r on today's row of its repository and model.
func (s *Store) Add(ctx context.Context, r Row) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO llm_usage(repository_id, day, model, calls, input_tokens, cached_tokens, output_tokens)
		VALUES (?, date('now'), ?, 1, ?, ?, ?)
		ON CONFLICT(repository_id, day, model) DO UPDATE SET calls = calls + 1, input_tokens = input_tokens + excluded.input_tokens,
			cached_tokens = cached_tokens + excluded.cached_tokens, output_tokens = output_tokens + excluded.output_tokens`,
		r.Repository, r.Model, r.In, r.Cached, r.Out)
	return err
}

// Totals sums each repository's spend on each model over the last days UTC
// days, today among them, or over all of them when days is 0; the largest
// input first.
func (s *Store) Totals(ctx context.Context, days int) ([]Total, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.repository_id, COALESCE(r.public_id, ''), COALESCE(r.owner, ''), COALESCE(r.name, ''),
			u.model, SUM(u.calls), SUM(u.input_tokens), SUM(u.cached_tokens), SUM(u.output_tokens)
		FROM llm_usage u LEFT JOIN repositories r ON r.id = u.repository_id
		WHERE ? = 0 OR u.day >= date('now', ?)
		GROUP BY u.repository_id, u.model
		ORDER BY SUM(u.input_tokens) DESC, u.repository_id, u.model`, days, fmt.Sprintf("-%d days", days-1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := []Total{}
	for rows.Next() {
		var t Total
		if err := rows.Scan(&t.RepoID, &t.RepoPublicID, &t.RepoOwner, &t.RepoName, &t.Model, &t.Calls, &t.Input, &t.Cached, &t.Output); err != nil {
			return nil, err
		}
		totals = append(totals, t)
	}
	return totals, rows.Err()
}
