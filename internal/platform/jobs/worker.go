package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"deepcode/internal/platform/events"
)

// runnable is the SQL condition for a queued job that may start now: its
// backoff has passed and its repository has no job running, so two jobs never
// touch one repository's clone at once.
const runnable = `j.status = 'queued' AND j.run_after <= CURRENT_TIMESTAMP
	AND NOT EXISTS (SELECT 1 FROM jobs r WHERE r.repository_id = j.repository_id AND r.status = 'running')`

// sweepEvery is how often Sweep looks for runnable jobs nobody was woken for.
var sweepEvery = 5 * time.Second

// Recover settles the jobs a previous process left running: queued again, or
// failed when that was their last attempt, so a job that kills the process
// cannot loop forever.
// ponytail: one process owns the database, so recovery needs no leases; add
// lease_until + heartbeats if a second process ever shares the file.
// Call before any worker runs.
func (q *Queue) Recover(ctx context.Context) error {
	_, err := q.DB.ExecContext(ctx, `
		UPDATE jobs SET updated_at = CURRENT_TIMESTAMP,
			status = CASE WHEN attempts >= ?1 THEN 'failed' ELSE 'queued' END,
			error = CASE WHEN attempts >= ?1 THEN 'the server stopped during the last attempt' ELSE error END
		WHERE status = 'running'`, maxAttempts)
	return err
}

// Register runs up to n workers. Each job.queued announcement is a wake-up,
// not an assignment: it starts a worker if one of the n slots is free, and
// that worker claims whatever is runnable. Watermill handles every message in
// its own goroutine, so the slots, not the subscription, bound how many jobs
// run at once. Jobs run under ctx (the process lifetime), not under the
// message, so shutting down hands running jobs back to the queue.
// Call before the bus runs.
func (q *Queue) Register(ctx context.Context, n int, handlers map[string]Handler) {
	q.slots = make(chan struct{}, n)
	events.Subscribe(q.Bus, "jobs.workers", events.TopicJobQueued, func(context.Context, events.JobQueued) error {
		q.drain(ctx, handlers)
		return nil
	})
}

// Sweep waits for the bus, then wakes the workers whenever a slot is free and
// a job is runnable: after a retry backoff, after a repository's previous job,
// for announcements published before the workers subscribed, and for ones
// that found every slot taken. Returns when ctx is cancelled.
func (q *Queue) Sweep(ctx context.Context) {
	select {
	case <-q.Bus.Running():
	case <-ctx.Done():
		return
	}
	tick := time.NewTicker(sweepEvery)
	defer tick.Stop()
	for {
		if len(q.slots) < cap(q.slots) && q.hasRunnable(ctx) {
			q.announce(0)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (q *Queue) hasRunnable(ctx context.Context) bool {
	var ok bool
	if err := q.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM jobs j WHERE `+runnable+`)`).Scan(&ok); err != nil && ctx.Err() == nil {
		log.Printf("jobs: sweep: %v", err)
	}
	return ok
}

// drain takes a free worker slot and runs runnable jobs, oldest first, until
// none is left. With every slot taken it returns at once: the busy workers
// claim the announced job when they finish theirs, and Sweep covers the one
// that was just giving up its slot.
func (q *Queue) drain(ctx context.Context, handlers map[string]Handler) {
	select {
	case q.slots <- struct{}{}:
		defer func() { <-q.slots }()
	default:
		return
	}
	for ctx.Err() == nil {
		job, ok := q.claim(ctx)
		if !ok {
			return
		}
		q.run(ctx, job, handlers[job.Kind])
	}
}

// claim atomically marks the oldest runnable job as running.
func (q *Queue) claim(ctx context.Context) (Job, bool) {
	j, err := scanJob(q.DB.QueryRowContext(ctx, `
		UPDATE jobs SET status = 'running', attempts = attempts + 1, error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT j.id FROM jobs j WHERE `+runnable+` ORDER BY j.id LIMIT 1)
		RETURNING `+jobColumns))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			log.Printf("jobs: claim: %v", err)
		}
		return Job{}, false
	}
	q.changed(events.TopicJobUpdated, j.ID)
	return j, true
}

// run executes one claimed job and records its outcome: success, a retry with
// backoff, a permanent failure, or a hand-back when the process shuts down.
// Then it prunes old jobs.
func (q *Queue) run(ctx context.Context, job Job, h Handler) {
	jctx, cancel := context.WithTimeout(ctx, q.Timeout)
	defer cancel()
	progress := Progress{q, job.ID}
	progress.Logf("%s %.7s: attempt %d of %d", job.Kind, job.CommitSHA, job.Attempts, maxAttempts)
	err := fmt.Errorf("no handler for job kind %q", job.Kind)
	if h != nil {
		err = safely(func() error { return h(jctx, job, progress) })
	}
	// ctx may already be cancelled (shutdown), so finish the bookkeeping regardless.
	bg := context.WithoutCancel(ctx)
	switch {
	case err == nil:
		progress.Logf("succeeded")
		_, err = q.DB.ExecContext(bg, `UPDATE jobs SET status = 'succeeded', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, job.ID)
	case ctx.Err() != nil:
		// Shutting down: hand the job back without spending an attempt. Work
		// already committed is reused when it resumes.
		progress.Logf("interrupted by shutdown; it resumes on the next start")
		_, err = q.DB.ExecContext(bg, `UPDATE jobs SET status = 'queued', attempts = attempts - 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, job.ID)
	case errors.As(err, new(permanent)) || job.Attempts >= maxAttempts:
		log.Printf("jobs: %s #%d failed: %v", job.Kind, job.ID, err)
		progress.Logf("failed: %v", err)
		_, err = q.DB.ExecContext(bg, `UPDATE jobs SET status = 'failed', error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, err.Error(), job.ID)
	default:
		backoff := 30 << (job.Attempts - 1) // 30s, 60s, ...
		log.Printf("jobs: %s #%d attempt %d failed, retrying in %ds: %v", job.Kind, job.ID, job.Attempts, backoff, err)
		progress.Logf("attempt failed, retrying in %ds: %v", backoff, err)
		_, err = q.DB.ExecContext(bg, `UPDATE jobs SET status = 'queued', error = ?, run_after = datetime('now', ?), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			err.Error(), fmt.Sprintf("+%d seconds", backoff), job.ID)
	}
	if err != nil {
		log.Printf("jobs: update #%d: %v", job.ID, err)
	}
	q.changed(events.TopicJobUpdated, job.ID)
	q.prune(bg)
}

// keepJobs is how long finished jobs and their output are kept.
const keepJobs = "-30 days"

// prune deletes up to 20 jobs (their output with them) that finished more
// than keepJobs ago. Each finished job prunes a few, so the tables stop
// growing without one long delete holding the write lock. A repository's
// newest job stays, however old: its page shows it.
func (q *Queue) prune(ctx context.Context) {
	if _, err := q.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id IN (
		SELECT j.id FROM jobs j WHERE j.status IN ('succeeded', 'failed') AND j.updated_at < datetime('now', ?)
			AND j.id < (SELECT max(n.id) FROM jobs n WHERE n.repository_id = j.repository_id)
		LIMIT 20)`, keepJobs); err != nil {
		log.Printf("jobs: prune: %v", err)
	}
}

// safely turns a handler panic into an error so one bad file cannot kill the server.
func safely(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return f()
}
