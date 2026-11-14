// Package store keeps the job queue in SQLite: the jobs table, the source of
// truth, and each job's output in job_logs.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Store runs the queue's SQL.
type Store struct{ DB *sql.DB }

// A job's status. The SQL in this package spells them out too.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

type Job struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	RepoID    int64  `json:"-"` // the row id; Listed carries the public one
	CommitSHA string `json:"commitSha"`
	Status    string `json:"status"`   // one of the statuses above
	Attempts  int    `json:"attempts"` // the runs that stored no work and were not cut by a server it could not reach
	Retries   int    `json:"retries"`  // the runs in a row cut by a server it could not reach
	Stage     string `json:"stage"`
	Progress  int    `json:"progress"`
	Total     int    `json:"total"`
	Error     string `json:"error"`    // in full: it may quote git, the model's API or paths, so the API hides it from users without jobs.view
	RunAfter  string `json:"runAfter"` // when a queued job may start: after the backoff of a retry
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Listed is a job with the public id and name of the repository it works on.
type Listed struct {
	Job
	RepoPublicID string `json:"repoId"`
	RepoOwner    string `json:"repoOwner"`
	RepoName     string `json:"repoName"`
}

// LogLine is one line of a job's output.
type LogLine struct {
	ID   int64  `json:"id"`
	At   string `json:"at"`
	Line string `json:"line"`
}

// Finished reports whether the job will not run again unless queued anew.
func (j Job) Finished() bool { return j.Status == StatusSucceeded || j.Status == StatusFailed }

var ErrNotFound = errors.New("找不到工作")

const jobColumns = `id, kind, repository_id, commit_sha, status, attempts, retries, stage, progress, total, error, run_after, created_at, updated_at`

// listedColumns are jobColumns of jobs j plus the public id and name of its
// repository r.
// ponytail: jobs read the repositories table that their repository_id
// references, to name it in one query; let callers name repositories through
// the catalog if jobs ever serve more than repositories.
var listedColumns = "j." + strings.ReplaceAll(jobColumns, ", ", ", j.") + ", r.public_id, r.owner, r.name"

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.RepoID, &j.CommitSHA, &j.Status, &j.Attempts, &j.Retries, &j.Stage, &j.Progress, &j.Total, &j.Error, &j.RunAfter, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func scanListed(row interface{ Scan(...any) error }) (Listed, error) {
	var l Listed
	j := &l.Job
	err := row.Scan(&j.ID, &j.Kind, &j.RepoID, &j.CommitSHA, &j.Status, &j.Attempts, &j.Retries, &j.Stage, &j.Progress, &j.Total, &j.Error, &j.RunAfter, &j.CreatedAt, &j.UpdatedAt,
		&l.RepoPublicID, &l.RepoOwner, &l.RepoName)
	return l, err
}

// Enqueue adds a job under key and returns its id. A finished job under key
// is queued again from scratch, and a trigger clears its old output; a queued
// or running one is left as it is.
func (s *Store) Enqueue(ctx context.Context, kind string, repoID int64, sha, key string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO jobs(kind, repository_id, commit_sha, idempotency_key) VALUES (?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO UPDATE SET status = 'queued', attempts = 0, retries = 0, saved = 0, error = '',
			stage = '', progress = 0, total = 0, run_after = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE jobs.status IN ('failed', 'succeeded')
		RETURNING id`, kind, repoID, sha, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.DB.QueryRowContext(ctx, `SELECT id FROM jobs WHERE idempotency_key = ?`, key).Scan(&id)
	}
	return id, err
}

// ByKey returns the job under idempotency key, or ErrNotFound.
func (s *Store) ByKey(ctx context.Context, key string) (Job, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE idempotency_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

func (s *Store) Get(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

// LatestOf returns the newest job of each repository in ids that has one,
// in one query. A queued or running job comes first: a re-queued older job is
// the one to watch.
func (s *Store) LatestOf(ctx context.Context, ids []int64) (map[int64]*Job, error) {
	list, _ := json.Marshal(ids)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+jobColumns+` FROM (
		SELECT *, row_number() OVER (PARTITION BY repository_id ORDER BY status IN ('queued', 'running') DESC, id DESC) AS n
		FROM jobs WHERE repository_id IN (SELECT value FROM json_each(?))) WHERE n = 1`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out[j.RepoID] = &j
	}
	return out, rows.Err()
}

// List returns up to limit of the newest jobs whose repository visible
// accepts, only those with status unless it is "".
// ponytail: reads newest first until limit jobs are visible, so a viewer of a
// few repositories may scan much of the table; filter by repository in SQL if
// that ever shows.
func (s *Store) List(ctx context.Context, status string, limit int, visible func(repoID int64) bool) ([]Listed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listedColumns+` FROM jobs j JOIN repositories r ON r.id = j.repository_id
		WHERE ?1 = '' OR j.status = ?1 ORDER BY j.id DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Listed{}
	for len(out) < limit && rows.Next() {
		l, err := scanListed(rows)
		if err != nil {
			return nil, err
		}
		if visible(l.RepoID) {
			out = append(out, l)
		}
	}
	return out, rows.Err()
}

// GetListed returns job id with the name of its repository.
func (s *Store) GetListed(ctx context.Context, id int64) (Listed, error) {
	l, err := scanListed(s.DB.QueryRowContext(ctx, `SELECT `+listedColumns+` FROM jobs j JOIN repositories r ON r.id = j.repository_id WHERE j.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Listed{}, ErrNotFound
	}
	return l, err
}

// Stage records job id's current stage and how far it has got.
func (s *Store) Stage(id int64, stage string, done, total int) error {
	_, err := s.DB.Exec(`UPDATE jobs SET stage = ?, progress = ?, total = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, stage, done, total, id)
	return err
}

// Log appends line to job id's output.
func (s *Store) Log(id int64, line string) error {
	_, err := s.DB.Exec(`INSERT INTO job_logs(job_id, line) VALUES (?, ?)`, id, line)
	return err
}

// Logs returns up to limit lines of job id's output after line after, oldest first.
func (s *Store) Logs(ctx context.Context, id, after int64, limit int) ([]LogLine, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, at, line FROM job_logs WHERE job_id = ? AND id > ? ORDER BY id LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []LogLine{}
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.ID, &l.At, &l.Line); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}
