package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// runnable is the SQL condition for a queued job that may start now: its
// backoff has passed and its repository has no job running, so two jobs never
// touch one repository's clone at once.
const runnable = `j.status = 'queued' AND j.run_after <= CURRENT_TIMESTAMP
	AND NOT EXISTS (SELECT 1 FROM jobs r WHERE r.repository_id = j.repository_id AND r.status = 'running')`

// Recover settles the jobs a previous process left running: queued again, or
// failed once they have run maxAttempts times. It returns how many it settled.
func (s *Store) Recover(ctx context.Context, maxAttempts int) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE jobs SET updated_at = CURRENT_TIMESTAMP,
			status = CASE WHEN attempts >= ?1 THEN 'failed' ELSE 'queued' END,
			error = CASE WHEN attempts >= ?1 THEN 'the server stopped during the last attempt' ELSE error END
		WHERE status = 'running'`, maxAttempts)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// HasRunnable reports whether a job is runnable.
func (s *Store) HasRunnable(ctx context.Context) (bool, error) {
	var ok bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM jobs j WHERE `+runnable+`)`).Scan(&ok)
	return ok, err
}

// Claim atomically marks the oldest runnable job as running; ok is false
// when none is runnable.
func (s *Store) Claim(ctx context.Context) (j Job, ok bool, err error) {
	j, err = scanJob(s.DB.QueryRowContext(ctx, `
		UPDATE jobs SET status = 'running', attempts = attempts + 1, error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT j.id FROM jobs j WHERE `+runnable+` ORDER BY j.id LIMIT 1)
		RETURNING `+jobColumns))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return j, err == nil, err
}

// Succeed marks job id succeeded.
func (s *Store) Succeed(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'succeeded', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// HandBack queues job id again without spending an attempt.
func (s *Store) HandBack(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'queued', attempts = attempts - 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// Fail marks job id failed for good, with message.
func (s *Store) Fail(ctx context.Context, id int64, message string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'failed', error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, message, id)
	return err
}

// Retry queues job id again to run seconds from now, with message.
func (s *Store) Retry(ctx context.Context, id int64, message string, seconds int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'queued', error = ?, run_after = datetime('now', ?), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		message, fmt.Sprintf("+%d seconds", seconds), id)
	return err
}

// Prune deletes up to 20 jobs (their output with them) that finished before
// keep, an SQLite date modifier such as "-30 days". A repository's newest job
// stays, however old.
func (s *Store) Prune(ctx context.Context, keep string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id IN (
		SELECT j.id FROM jobs j WHERE j.status IN ('succeeded', 'failed') AND j.updated_at < datetime('now', ?)
			AND j.id < (SELECT max(n.id) FROM jobs n WHERE n.repository_id = j.repository_id)
		LIMIT 20)`, keep)
	return err
}
