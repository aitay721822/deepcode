package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs/store"
)

// Queue keeps jobs in SQLite (the source of truth) and announces them on the
// event bus, where the workers subscribe.
type Queue struct {
	Store   *store.Store
	Bus     *events.Bus
	Timeout time.Duration // per job
	slots   chan struct{} // one per worker, held while it drains the queue

	// mu orders claims, stops and outcomes, so Stop finds a job either queued,
	// in running, or with its outcome stored.
	mu      sync.Mutex
	running map[int64]runningJob // the jobs this process has claimed, until their outcome is stored
}

// runningJob is the context a claimed job runs under, and how Stop cancels it.
type runningJob struct {
	ctx  context.Context
	stop context.CancelCauseFunc
}

// maxAttempts is how many attempts that stored no work (Progress.Saved) a job
// runs before it fails for good.
const maxAttempts = 3

func New(st *store.Store, bus *events.Bus, timeout time.Duration) *Queue {
	return &Queue{Store: st, Bus: bus, Timeout: timeout}
}

func (q *Queue) EnqueueIndex(ctx context.Context, repoID int64, sha, embedModel string) (Job, error) {
	return q.enqueue(ctx, KindIndex, repoID, sha, indexKey(repoID, sha, embedModel))
}

// IndexJob returns the index job of repoID at sha for embedModel, the one
// EnqueueIndex would queue again, or ErrNotFound.
func (q *Queue) IndexJob(ctx context.Context, repoID int64, sha, embedModel string) (Job, error) {
	return q.Store.ByKey(ctx, indexKey(repoID, sha, embedModel))
}

func indexKey(repoID int64, sha, embedModel string) string {
	return fmt.Sprintf("index:%d:%s:v%d:%s", repoID, sha, PipelineVersion, embedModel)
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
	id, err := q.Store.Enqueue(ctx, kind, repoID, sha, key)
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

var ErrNotFound = store.ErrNotFound

// ErrFinished: the job has ended, so there is nothing to stop.
var ErrFinished = errors.New("工作已結束")

func (q *Queue) Get(ctx context.Context, id int64) (Job, error) { return q.Store.Get(ctx, id) }

// LatestOf returns the newest job of each repository in ids that has one,
// in one query. A queued or running job comes first: a re-queued older job is
// the one to watch.
func (q *Queue) LatestOf(ctx context.Context, ids []int64) (map[int64]*Job, error) {
	return q.Store.LatestOf(ctx, ids)
}

// List returns up to limit of the newest jobs whose repository visible
// accepts, only those with status unless it is "".
func (q *Queue) List(ctx context.Context, status string, limit int, visible func(repoID int64) bool) ([]Listed, error) {
	return q.Store.List(ctx, status, limit, visible)
}

// GetListed returns job id with the name of its repository.
func (q *Queue) GetListed(ctx context.Context, id int64) (Listed, error) {
	return q.Store.GetListed(ctx, id)
}
