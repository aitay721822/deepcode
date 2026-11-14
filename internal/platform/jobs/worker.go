package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"deepcode/internal/platform/events"
)

// sweepEvery is how often Sweep looks for runnable jobs nobody was woken for.
var sweepEvery = 5 * time.Second

// Recover settles the jobs a previous process left running: queued again,
// to resume from the work they stored, or failed when that was their last
// attempt and it stored none, so a job that kills the process cannot loop
// forever while one killed as it makes progress (a reboot, an operator's
// kill) is never failed for it.
// ponytail: one process owns the database, so recovery needs no leases; add
// lease_until + heartbeats if a second process ever shares the file.
// Call before any worker runs.
func (q *Queue) Recover(ctx context.Context) error {
	n, err := q.Store.Recover(ctx, maxAttempts)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("jobs: %d were running when the server stopped: queued again, or failed if that was their last attempt", n)
	}
	return nil
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
	ok, err := q.Store.HasRunnable(ctx)
	if err != nil && ctx.Err() == nil {
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

// claim atomically marks the oldest runnable job as running, and keeps a
// context under ctx for run to run it in, which Stop cancels.
func (q *Queue) claim(ctx context.Context) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok, err := q.Store.Claim(ctx)
	if err != nil && ctx.Err() == nil {
		log.Printf("jobs: claim: %v", err)
	}
	if !ok {
		return Job{}, false
	}
	if q.running == nil {
		q.running = map[int64]runningJob{}
	}
	jctx, stop := context.WithCancelCause(ctx)
	q.running[j.ID] = runningJob{jctx, stop}
	q.changed(events.TopicJobUpdated, j.ID)
	return j, true
}

// Stop stops job id for user by: a queued job fails at once; a running one is
// cancelled and fails once its handler returns, so its repository is never
// touched by two jobs. Either keeps the work it stored, which a Sync resumes
// from. ErrFinished when the job has already ended.
func (q *Queue) Stop(ctx context.Context, id int64, by string) (Job, error) {
	why := stopped{by}
	q.mu.Lock()
	queued, err := q.Store.Stop(ctx, id, why.Error())
	if queued { // under q.mu, so before any run of it a Sync queues
		Progress{q: q, id: id}.Logf("failed: %v", why)
	}
	r, running := q.running[id]
	if running {
		r.stop(why)
	}
	q.mu.Unlock()
	if err != nil {
		return Job{}, err
	}
	if queued {
		q.changed(events.TopicJobUpdated, id)
	}
	j, err := q.Get(ctx, id)
	if err == nil && !queued && !running {
		err = ErrFinished
	}
	return j, err
}

// run executes one claimed job and records its outcome: success, a hand-back
// when the process shuts down, a permanent failure, or a retry with backoff.
// An attempt that stored work a later one skips (Progress.Saved) does not
// count, nor does one cut by a server the job could not reach (Transient),
// which is retried for about a day instead; so maxAttempts bounds only the
// attempts that got nowhere. A job a user stopped fails. Then it prunes old
// jobs. job is one claim(ctx) returned.
func (q *Queue) run(ctx context.Context, job Job, h Handler) {
	q.mu.Lock()
	r := q.running[job.ID]
	q.mu.Unlock()
	defer r.stop(nil)
	jctx, cancel := context.WithTimeoutCause(r.ctx, q.Timeout, fmt.Errorf("the job ran past DEEPCODE_JOB_TIMEOUT (%s): %w", q.Timeout, context.DeadlineExceeded))
	defer cancel()
	start := time.Now()
	progress := Progress{q: q, id: job.ID, saved: new(atomic.Int32)}
	progress.Logf("%s %.7s: attempt %d of %d, counted unless it stores work or cannot reach a server", job.Kind, job.CommitSHA, job.Attempts, maxAttempts)
	err := fmt.Errorf("no handler for job kind %q", job.Kind)
	if h != nil {
		err = safely(func() error { return h(jctx, job, progress) })
	}
	q.mu.Lock() // a Stop from here on finds the outcome stored, not the job running
	delete(q.running, job.ID)
	// The job's own timeout or stop, named, rather than what the step it cut short made of it; a stop before a
	// shutdown still fails the job.
	if cause := context.Cause(jctx); err != nil && jctx.Err() != nil && (ctx.Err() == nil || errors.As(cause, new(stopped))) {
		err = cause
	}
	// ctx may already be cancelled (shutdown), so finish the bookkeeping regardless.
	bg := context.WithoutCancel(ctx)
	attempts, retries, backoff := job.Attempts, job.Retries, 30<<max(job.Attempts-1, 0) // 30s, 60s
	if progress.saved.Load() != savedNone {
		attempts, retries, backoff = attempts-1, 0, 30
	}
	switch {
	case err == nil:
		log.Printf("jobs: %s #%d (repository %d) succeeded in %s", job.Kind, job.ID, job.RepoID, time.Since(start).Round(time.Second))
		progress.Logf("succeeded")
		err = q.Store.Succeed(bg, job.ID)
	case errors.As(err, new(stopped)):
		err = q.fail(bg, job, progress, err)
	case ctx.Err() != nil:
		// Shutting down: hand the job back without spending an attempt. Work
		// already committed is reused when it resumes.
		progress.Logf("interrupted by shutdown; it resumes on the next start")
		err = q.Store.HandBack(bg, job.ID)
	case errors.As(err, new(permanent)):
		err = q.fail(bg, job, progress, err)
	case errors.As(err, new(transient)):
		if retries++; retries > maxRetries {
			err = q.fail(bg, job, progress, fmt.Errorf("still failing after %d retries over about a day: %w", maxRetries, err))
			break
		}
		wait := transientBackoff(retries)
		log.Printf("jobs: %s #%d (repository %d) could not reach a server (retry %d of %d in %s): %v", job.Kind, job.ID, job.RepoID, retries, maxRetries, wait, err)
		progress.Logf("a server could not be reached; the attempt does not count (retry %d of %d in %s): %v", retries, maxRetries, wait, err)
		err = q.Store.Retry(bg, job.ID, err.Error(), int(wait.Seconds()), job.Attempts-1, retries)
	case attempts >= maxAttempts:
		err = q.fail(bg, job, progress, err)
	default:
		log.Printf("jobs: %s #%d (repository %d) attempt %d failed, retrying in %ds: %v", job.Kind, job.ID, job.RepoID, job.Attempts, backoff, err)
		progress.Logf("attempt failed, retrying in %ds: %v", backoff, err)
		err = q.Store.Retry(bg, job.ID, err.Error(), backoff, attempts, retries)
	}
	q.mu.Unlock()
	if err != nil {
		log.Printf("jobs: update #%d: %v", job.ID, err)
	}
	q.changed(events.TopicJobUpdated, job.ID)
	q.prune(bg)
}

// fail marks job failed for good, with err.
func (q *Queue) fail(ctx context.Context, job Job, progress Progress, err error) error {
	log.Printf("jobs: %s #%d (repository %d) failed: %v", job.Kind, job.ID, job.RepoID, err)
	progress.Logf("failed: %v", err)
	return q.Store.Fail(ctx, job.ID, err.Error())
}

// maxRetries is how often a job cut by a server it could not reach runs again
// before it fails: about a day, by transientBackoff.
const maxRetries = 50

// transientBackoff is the wait before retry n of a job cut by a server it
// could not reach: 30s, doubling up to 30 minutes.
func transientBackoff(n int) time.Duration {
	return min(30*time.Second<<min(n-1, 6), 30*time.Minute)
}

// keepJobs is how long finished jobs and their output are kept.
const keepJobs = "-30 days"

// prune deletes up to 20 jobs (their output with them) that finished more
// than keepJobs ago. Each finished job prunes a few, so the tables stop
// growing without one long delete holding the write lock. A repository's
// newest job stays, however old: its page shows it.
func (q *Queue) prune(ctx context.Context) {
	if err := q.Store.Prune(ctx, keepJobs); err != nil {
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
