package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"deepcode/internal/platform/events"
)

// Queue keeps jobs in SQLite (the source of truth) and announces them on the
// event bus, where the workers subscribe.
type Queue struct {
	DB      *sql.DB
	Bus     *events.Bus
	Timeout time.Duration // per job
	slots   chan struct{} // one per worker, held while it drains the queue
}

// maxAttempts is how often a job runs before it fails for good.
const maxAttempts = 3

func New(db *sql.DB, bus *events.Bus, timeout time.Duration) *Queue {
	return &Queue{DB: db, Bus: bus, Timeout: timeout}
}

func (q *Queue) EnqueueIndex(ctx context.Context, repoID int64, sha, embedModel string) (Job, error) {
	return q.enqueue(ctx, KindIndex, repoID, sha, fmt.Sprintf("index:%d:%s:v%d:%s", repoID, sha, PipelineVersion, embedModel))
}

func (q *Queue) EnqueueWiki(ctx context.Context, repoID int64, sha string) (Job, error) {
	return q.enqueue(ctx, KindWiki, repoID, sha, fmt.Sprintf("wiki:%d:%s:v%d", repoID, sha, PipelineVersion))
}

// enqueue is idempotent on key while the job is queued or running: the
// existing job is returned as is. A finished one is queued again from
// scratch: one that failed for good, or one that succeeded but is asked for
// again (a force-push back to an older commit, an embedding model switched
// back), since callers only ask when the work is needed; a trigger clears its
// old output. The row is committed before the job is announced.
func (q *Queue) enqueue(ctx context.Context, kind string, repoID int64, sha, key string) (Job, error) {
	var id int64
	err := q.DB.QueryRowContext(ctx, `
		INSERT INTO jobs(kind, repository_id, commit_sha, idempotency_key) VALUES (?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO UPDATE SET status = 'queued', attempts = 0, error = '',
			stage = '', progress = 0, total = 0, run_after = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE jobs.status IN ('failed', 'succeeded')
		RETURNING id`, kind, repoID, sha, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = q.DB.QueryRowContext(ctx, `SELECT id FROM jobs WHERE idempotency_key = ?`, key).Scan(&id)
	}
	if err != nil {
		return Job{}, err
	}
	q.announce(id)
	q.changed(events.TopicJobUpdated, id)
	return q.Get(ctx, id)
}

// announce publishes job.queued; a lost announcement is picked up by Sweep.
func (q *Queue) announce(id int64) {
	if err := q.Bus.Publish(events.TopicJobQueued, events.JobQueued{}); err != nil {
		log.Printf("jobs: announce #%d: %v", id, err)
	}
}

const jobColumns = `id, kind, repository_id, commit_sha, status, attempts, stage, progress, total, error, created_at, updated_at`

// listedColumns are jobColumns of jobs j plus the name of its repository r.
// ponytail: jobs read the repositories table that their repository_id
// references, to name it in one query; let callers name repositories through
// the catalog if jobs ever serve more than repositories.
var listedColumns = "j." + strings.ReplaceAll(jobColumns, ", ", ", j.") + ", r.owner, r.name"

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.RepoID, &j.CommitSHA, &j.Status, &j.Attempts, &j.Stage, &j.Progress, &j.Total, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

var ErrNotFound = errors.New("job not found")

func (q *Queue) Get(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(q.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

// LatestOf returns the newest job of each repository in ids that has one,
// in one query. A queued or running job comes first: a re-queued older job is
// the one to watch.
func (q *Queue) LatestOf(ctx context.Context, ids []int64) (map[int64]*Job, error) {
	list, _ := json.Marshal(ids)
	rows, err := q.DB.QueryContext(ctx, `SELECT `+jobColumns+` FROM (
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
func (q *Queue) List(ctx context.Context, status string, limit int, visible func(repoID int64) bool) ([]Listed, error) {
	rows, err := q.DB.QueryContext(ctx, `SELECT `+listedColumns+` FROM jobs j JOIN repositories r ON r.id = j.repository_id
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
func (q *Queue) GetListed(ctx context.Context, id int64) (Listed, error) {
	l, err := scanListed(q.DB.QueryRowContext(ctx, `SELECT `+listedColumns+` FROM jobs j JOIN repositories r ON r.id = j.repository_id WHERE j.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Listed{}, ErrNotFound
	}
	return l, err
}

func scanListed(row interface{ Scan(...any) error }) (Listed, error) {
	var l Listed
	j := &l.Job
	err := row.Scan(&j.ID, &j.Kind, &j.RepoID, &j.CommitSHA, &j.Status, &j.Attempts, &j.Stage, &j.Progress, &j.Total, &j.Error, &j.CreatedAt, &j.UpdatedAt,
		&l.RepoOwner, &l.RepoName)
	return l, err
}
