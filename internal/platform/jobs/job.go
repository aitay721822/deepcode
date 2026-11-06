// Package jobs is a durable queue in SQLite worked by an in-process pool.
// The SQLite row is the source of truth; events only cut latency.
package jobs

import "context"

const (
	KindIndex = "index"
	KindWiki  = "wiki"

	// PipelineVersion is part of every idempotency key, so jobs queued after a
	// bump get rows of their own. It re-processes nothing by itself: a commit
	// indexed before keeps its snapshot and wiki, and a new snapshot copies the
	// files unchanged since the last one instead of parsing them again.
	PipelineVersion = 1
)

// A job's status. SQL in this package spells them out too.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Statuses lists every status in the order a job passes through them.
var Statuses = []string{StatusQueued, StatusRunning, StatusSucceeded, StatusFailed}

type Job struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	RepoID    int64  `json:"repoId"`
	CommitSHA string `json:"commitSha"`
	Status    string `json:"status"` // one of Statuses
	Attempts  int    `json:"attempts"`
	Stage     string `json:"stage"`
	Progress  int    `json:"progress"`
	Total     int    `json:"total"`
	Error     string `json:"error"` // in full: it may quote git, the model's API or paths, so the API hides it from users without jobs.view
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Listed is a job with the name of the repository it works on.
type Listed struct {
	Job
	RepoOwner string `json:"repoOwner"`
	RepoName  string `json:"repoName"`
}

// LogLine is one line of a job's output.
type LogLine struct {
	ID   int64  `json:"id"`
	At   string `json:"at"`
	Line string `json:"line"`
}

// Finished reports whether the job will not run again unless queued anew.
func (j Job) Finished() bool { return j.Status == StatusSucceeded || j.Status == StatusFailed }

type Handler func(ctx context.Context, job Job, progress Progress) error

type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// Permanent marks an error that retrying cannot fix, such as a size limit.
func Permanent(err error) error { return permanent{err} }
