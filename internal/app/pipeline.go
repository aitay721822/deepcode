package app

import (
	"context"
	"errors"

	"deepcode/internal/knowledge/wiki"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/index/indexer"
)

// pipeline is the background workflow, driven by events:
//
//	job.queued (index)  → indexer.Run → snapshot.published
//	snapshot.published  → wiki job queued → job.queued (wiki) → wiki.Run
//
// Every step's state is in SQLite first; the events only say "go".
type pipeline struct {
	queue   *jobs.Queue
	indexer *indexer.Indexer
	wiki    *wiki.Generator // nil when no chat model is configured
}

// handlers runs each job kind.
func (p pipeline) handlers() map[string]jobs.Handler {
	return map[string]jobs.Handler{
		jobs.KindIndex: func(ctx context.Context, job jobs.Job, progress jobs.Progress) error {
			return p.indexer.Run(ctx, job.RepoID, job.CommitSHA, progress)
		},
		jobs.KindWiki: func(ctx context.Context, job jobs.Job, progress jobs.Progress) error {
			if p.wiki == nil {
				return jobs.Permanent(errors.New("no chat model is configured: set DEEPCODE_CHAT_MODEL"))
			}
			return p.wiki.Run(ctx, job.RepoID, job.CommitSHA, progress)
		},
	}
}

// subscribe queues the wiki of every published snapshot. Should the process
// stop before that, Sync queues the missing wiki.
func (p pipeline) subscribe(bus *events.Bus) {
	if p.wiki == nil {
		return
	}
	events.Subscribe(bus, "wiki.on-snapshot-published", events.TopicSnapshotPublished,
		func(ctx context.Context, e events.SnapshotPublished) error {
			_, err := p.queue.EnqueueWiki(ctx, e.RepoID, e.CommitSHA)
			return err
		})
}
