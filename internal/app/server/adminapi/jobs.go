package adminapi

import (
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
)

// firstLogs is how many log lines getJob returns; the output stream sends the rest.
const firstLogs = 5000

func (a *API) listJobs(c *gin.Context) {
	status := c.Query("status")
	if status != "" && !slices.Contains(jobs.Statuses, status) {
		httpx.Fail(c, http.StatusBadRequest, "status 必須是下列其中之一："+strings.Join(jobs.Statuses, ", "))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 500 {
		httpx.Fail(c, http.StatusBadRequest, "limit 必須介於 1 到 500")
		return
	}
	// A job's output quotes its repository's code and wiki, so a job is as
	// private as its repository.
	list, err := a.Queue.List(c.Request.Context(), status, limit, func(repoID int64) bool { return httpx.CanRead(c, repoID) })
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// job reads job :id, or answers 404 when the viewer may not read its repository.
func (a *API) job(c *gin.Context) (jobs.Listed, bool) {
	id, ok := httpx.PathID(c)
	if !ok {
		return jobs.Listed{}, false
	}
	job, err := a.Queue.GetListed(c.Request.Context(), id)
	if err != nil {
		httpx.DomainError(c, err)
		return jobs.Listed{}, false
	}
	return job, httpx.Readable(c, job.RepoID, jobs.ErrNotFound)
}

func (a *API) getJob(c *gin.Context) {
	if job, ok := a.job(c); ok {
		logs, err := a.Queue.Logs(c.Request.Context(), job.ID, 0, firstLogs)
		reply(c, http.StatusOK, gin.H{"job": job, "logs": logs}, err)
	}
}

// jobEvents streams every change of a job the viewer may read as a "job"
// event, until the client leaves or the viewer loses jobs.view.
func (a *API) jobEvents(c *gin.Context) {
	stream := httpx.OpenStream(c)
	ctx, stop := stream.Watch(c, a.Sessions, func(p *session.Principal) bool { return p.Can(role.JobsView) })
	defer stop()
	changes, err := events.Listen[events.JobChanged](ctx, a.Bus, events.TopicJobUpdated)
	if err != nil {
		return // the bus is closing with the server
	}
	for e := range changes { // until ctx ends
		if job, err := a.Queue.GetListed(ctx, e.JobID); err == nil && httpx.CanRead(c, job.RepoID) {
			stream.Send("job", 0, job)
		}
	}
}

// jobOutput streams job :id's output after line ?after= (or the
// Last-Event-ID of a reconnecting EventSource) as "log" events and its
// changes as "job" events; once the job has finished, an "end" event, and
// then it closes. It stops when the viewer may no longer read the job.
func (a *API) jobOutput(c *gin.Context) {
	job, ok := a.job(c)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(c.Query("after"), 10, 64)
	if last, err := strconv.ParseInt(c.GetHeader("Last-Event-ID"), 10, 64); err == nil && last > after {
		after = last
	}
	stream := httpx.OpenStream(c)
	ctx, stop := stream.Watch(c, a.Sessions, func(p *session.Principal) bool {
		return p.Can(role.JobsView) && p.CanOn(role.RepoRead, job.RepoID)
	})
	defer stop()
	end, err := a.Queue.Follow(ctx, job.ID, after,
		func(l jobs.LogLine) { stream.Send("log", l.ID, l) },
		func(j jobs.Listed) { stream.Send("job", 0, j) })
	switch {
	case err == nil:
		stream.Send("end", 0, end)
	case ctx.Err() == nil:
		log.Printf("api: output of job %d: %v", job.ID, err)
	}
}
