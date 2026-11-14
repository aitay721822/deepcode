package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"

	"deepcode/internal/platform/events"
)

const (
	maxLine  = 2000 // bytes in one log line; longer ones are cut
	maxLines = 1000 // lines one Lines sink logs; the rest is left out
	logPage  = 500  // log lines Follow reads per query
)

// Progress records what a running job is doing: its stage and how far it has
// got, and lines of output. Each change is announced on the bus for the admin
// console. The zero value records nothing, for callers outside a job.
type Progress struct {
	q      *Queue
	id     int64
	prefix string        // starts each line of output
	saved  *atomic.Int32 // what the attempt stored (Saved); nil outside a run
}

// With is p with each line of output starting with prefix: the lines of one
// of several things the job does at a time.
func (p Progress) With(prefix string) Progress {
	p.prefix += prefix
	return p
}

// Stage records the current stage and how far it has got.
func (p Progress) Stage(stage string, done, total int) {
	if p.q == nil {
		return
	}
	if err := p.q.Store.Stage(p.id, stage, done, total); err != nil {
		log.Printf("jobs: progress #%d: %v", p.id, err)
	}
	p.q.changed(events.TopicJobUpdated, p.id)
}

// Saved records that the job stored work a later attempt skips, such as a
// batch of files or vectors or a page, so this attempt does not count against
// its attempts however it ends, a killed process included. The database is
// told once, or again by the next call when telling it failed.
func (p Progress) Saved() {
	if p.q == nil || p.saved == nil || p.saved.Load() == savedTold {
		return
	}
	p.saved.CompareAndSwap(savedNone, savedWork)
	if err := p.q.Store.Saved(p.id); err != nil {
		log.Printf("jobs: progress #%d: %v", p.id, err)
		return
	}
	p.saved.Store(savedTold)
}

// What an attempt stored, as Progress.saved holds it.
const (
	savedNone int32 = iota
	savedWork       // work a later attempt skips
	savedTold       // that, and the database knows it
)

// Logf appends one line to the job's output.
func (p Progress) Logf(format string, args ...any) {
	if p.q == nil {
		return
	}
	line := p.prefix + fmt.Sprintf(format, args...)
	if len(line) > maxLine {
		line = strings.ToValidUTF8(line[:maxLine], "") + "…"
	}
	if err := p.q.Store.Log(p.id, line); err != nil {
		log.Printf("jobs: log #%d: %v", p.id, err)
	}
	p.q.changed(events.TopicJobOutput, p.id)
}

// Lines returns a sink for streamed text, such as a model's answer, that logs
// each line as it completes, and a flush for the last, unfinished one. Past
// maxLines lines it logs one saying the rest is left out, and takes no more.
func (p Progress) Lines() (write func(string), flush func()) {
	var b strings.Builder
	n := 0 // lines ended
	end := func() {
		if n++; n > maxLines {
			p.Logf("… the rest is left out of the log, past %d lines", maxLines)
		} else {
			p.Logf("%s", b.String())
		}
		b.Reset()
	}
	flush = func() {
		if b.Len() > 0 {
			end()
		}
	}
	write = func(s string) {
		for n <= maxLines {
			line, rest, ok := strings.Cut(s, "\n")
			b.WriteString(line)
			if !ok {
				return
			}
			end()
			s = rest
		}
	}
	return write, flush
}

// Logs returns up to limit lines of job id's output after line after, oldest first.
func (q *Queue) Logs(ctx context.Context, id, after int64, limit int) ([]LogLine, error) {
	return q.Store.Logs(ctx, id, after, limit)
}

// Follow passes job id's output after line after to line, oldest first, and
// the job to job each time it changes, starting with the job as it is now,
// until the job has finished and its last line has been passed; then it
// returns the finished job. It stops early with ctx's error. Lines are always
// read from SQLite, so none is lost or repeated: events only say when to read.
func (q *Queue) Follow(ctx context.Context, id, after int64, line func(LogLine), job func(Listed)) (Listed, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // ends the listeners
	// Listen before reading, so that no change after the read goes unnoticed.
	output, err := events.Listen[events.JobChanged](ctx, q.Bus, events.TopicJobOutput)
	if err != nil {
		return Listed{}, err
	}
	updates, err := events.Listen[events.JobChanged](ctx, q.Bus, events.TopicJobUpdated)
	if err != nil {
		return Listed{}, err
	}
	current, err := q.GetListed(ctx, id)
	if err != nil {
		return Listed{}, err
	}
	job(current)
	for {
		// The job was read before these lines, so once it has finished they are its last.
		for {
			lines, err := q.Logs(ctx, id, after, logPage)
			if err != nil {
				return Listed{}, err
			}
			for _, l := range lines {
				line(l)
				after = l.ID
			}
			if len(lines) < logPage {
				break
			}
		}
		if current.Finished() {
			return current, nil
		}
		updated, err := cue(ctx, id, output, updates)
		if err != nil {
			return Listed{}, err
		}
		if updated {
			if current, err = q.GetListed(ctx, id); err != nil {
				return Listed{}, err
			}
			job(current)
		}
	}
}

// cue waits for the next event about job id: updated is true when its row
// changed, false when it has new output.
func cue(ctx context.Context, id int64, output, updates <-chan events.JobChanged) (updated bool, err error) {
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case e, ok := <-output:
			if !ok {
				return false, errBusClosed
			}
			if e.JobID == id {
				return false, nil
			}
		case e, ok := <-updates:
			if !ok {
				return false, errBusClosed
			}
			if e.JobID == id {
				return true, nil
			}
		}
	}
}

var errBusClosed = errors.New("the event bus closed")

// changed tells the admin console that job id's row or output changed. It is
// only a cue to re-read SQLite, so a failure (the bus closing at shutdown) is
// ignored.
func (q *Queue) changed(topic string, id int64) {
	q.Bus.Publish(topic, events.JobChanged{JobID: id})
}
