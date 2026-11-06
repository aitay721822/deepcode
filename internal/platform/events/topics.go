package events

// Topics and their payloads: the contracts between packages.
const (
	// TopicJobQueued wakes the job workers: a job was queued or may now be runnable.
	TopicJobQueued = "job.queued"
	// TopicJobUpdated says a job's status or progress changed.
	TopicJobUpdated = "job.updated"
	// TopicJobOutput says a job wrote lines to its log.
	TopicJobOutput = "job.output"
	// TopicSnapshotPublished announces that a repository snapshot became READY.
	TopicSnapshotPublished = "snapshot.published"
)

// JobQueued carries nothing: the workers read the queue itself.
type JobQueued struct{}

// JobChanged is the payload of job.updated and job.output: which job to read again.
type JobChanged struct {
	JobID int64 `json:"jobId"`
}

// SnapshotPublished is the payload of snapshot.published.
type SnapshotPublished struct {
	RepoID    int64  `json:"repoId"`
	CommitSHA string `json:"commitSha"`
}
