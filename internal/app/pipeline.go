package app

import (
	"context"
	"errors"
	"log"
	"time"

	"deepcode/internal/knowledge/wiki"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/index/indexer"
)

// pipeline is the background workflow, driven by events:
//
//	poll (every syncEvery) → index job queued for a moved HEAD
//	job.queued (index)     → indexer.Run → snapshot.published
//	snapshot.published     → wiki job queued → job.queued (wiki) → wiki.Run
//
// Every step's state is in SQLite first; the events only say "go".
type pipeline struct {
	queue     *jobs.Queue
	indexer   *indexer.Indexer
	wiki      *wiki.Generator // nil when no chat model is configured
	catalog   *catalog.Catalog
	syncEvery time.Duration // 0: no polling
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

// fillTerms splits the identifiers of the chunks an older version stored
// without terms, beside the server: until it is done they are found by their
// content alone, and a stop leaves the rest to the next start.
func (p pipeline) fillTerms(ctx context.Context) {
	n, err := p.indexer.FillTerms(ctx)
	if n > 0 {
		log.Printf("indexer: split the identifiers of %d chunks stored before this version", n)
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("indexer: splitting the identifiers of earlier chunks stopped (the next start goes on): %v", err)
	}
}

// poll checks every repository's remote HEAD each syncEvery until ctx is
// cancelled, and does nothing when syncEvery is 0.
func (p pipeline) poll(ctx context.Context) {
	if p.syncEvery <= 0 {
		return
	}
	tick := time.NewTicker(p.syncEvery)
	defer tick.Stop()
	failing := map[int64]string{} // the error last logged for each repository
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.pollRound(ctx, failing)
		}
	}
}

// pollRound polls every repository once; the next round tries again after a
// failure. An error is logged unless it is the one last logged for its
// repository, so a git server that stays down is logged once, and so is its
// return. A panic is logged instead of stopping the server.
func (p pipeline) pollRound(ctx context.Context, failing map[int64]string) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("poll: panic: %v", v)
		}
	}()
	repos, err := p.catalog.List(ctx, nil)
	if err != nil {
		log.Printf("poll: %v", err)
		return
	}
	for _, r := range repos {
		job, err := p.catalog.Poll(ctx, r.ID)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			if failing[r.ID] != err.Error() {
				log.Printf("poll %s/%s: %v", r.Owner, r.Name, err)
			}
			failing[r.ID] = err.Error()
		default:
			if _, ok := failing[r.ID]; ok {
				log.Printf("poll %s/%s: working again", r.Owner, r.Name)
				delete(failing, r.ID)
			}
			if job != nil {
				log.Printf("poll %s/%s: indexing the new commit %.7s", r.Owner, r.Name, job.CommitSHA)
			}
		}
	}
}
