// Package jobs is a durable queue in SQLite worked by an in-process pool.
// The SQLite row is the source of truth; events only cut latency.
package jobs

import (
	"context"

	"deepcode/internal/platform/jobs/store"
)

const (
	KindIndex = "index"
	KindWiki  = "wiki"

	// PipelineVersion is part of every idempotency key, so jobs queued after a
	// bump get rows of their own. It re-processes nothing by itself: a commit
	// indexed before keeps its snapshot and wiki, and a new snapshot copies the
	// files unchanged since the last one instead of parsing them again.
	PipelineVersion = 1
)

// A job's status. The store's SQL spells them out too.
const (
	StatusQueued    = store.StatusQueued
	StatusRunning   = store.StatusRunning
	StatusSucceeded = store.StatusSucceeded
	StatusFailed    = store.StatusFailed
)

// Statuses lists every status in the order a job passes through them.
var Statuses = []string{StatusQueued, StatusRunning, StatusSucceeded, StatusFailed}

type (
	Job     = store.Job
	Listed  = store.Listed  // a job with the public id and name of its repository
	LogLine = store.LogLine // one line of a job's output
)

type Handler func(ctx context.Context, job Job, progress Progress) error

type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// Permanent marks an error that retrying cannot fix, such as a size limit.
func Permanent(err error) error { return permanent{err} }

type transient struct{ error }

func (t transient) Unwrap() error { return t.error }

// Transient marks an error from a server the job could not reach, or that
// failed for now (a model server down, a git transfer cut): the job runs
// again after a backoff, its attempts unspent, for about a day.
func Transient(err error) error { return transient{err} }

// stopped is why a job a user stopped ended: it fails, saying who stopped it.
type stopped struct{ by string }

func (s stopped) Error() string { return "已由 " + s.by + " 停止" }
