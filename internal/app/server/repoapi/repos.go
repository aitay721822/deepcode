package repoapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog"
)

// repoView is a repository as the user sees it: with what they may do on it,
// since each role grants its permissions on its own repositories.
type repoView struct {
	catalog.Repo
	Permissions []role.Permission `json:"permissions"`
}

func view(c *gin.Context, r catalog.Repo) repoView {
	jobView(c, r.Job)
	return repoView{r, httpx.Principal(c).On(r.ID)}
}

// listRepos returns the repositories the user may read.
func (a *API) listRepos(c *gin.Context) {
	list, err := a.Catalog.List(c.Request.Context(), func(id int64) bool { return httpx.CanRead(c, id) })
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	out := make([]repoView, len(list))
	for i, r := range list {
		out[i] = view(c, r)
	}
	c.JSON(http.StatusOK, out)
}

func (a *API) getRepo(c *gin.Context) {
	repo, err := a.Catalog.Get(c.Request.Context(), httpx.RepoID(c))
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, view(c, repo))
}

// importRepo answers 202 when it queued a new repository, which joins the
// importer's roles, and 200 for one imported before that the user may read.
func (a *API) importRepo(c *gin.Context) {
	var body struct {
		URL      string `json:"url"`
		ServerID int64  `json:"serverId"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	repo, created, err := a.Importer.Import(c.Request.Context(), httpx.Principal(c), body.URL, body.ServerID)
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	jobView(c, repo.Job)
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	c.JSON(status, repo)
}

func (a *API) syncRepo(c *gin.Context) {
	job, err := a.Catalog.Sync(c.Request.Context(), httpx.RepoID(c))
	switch {
	case err != nil:
		httpx.DomainError(c, err)
	case job == nil:
		c.JSON(http.StatusOK, gin.H{"upToDate": true})
	default:
		jobView(c, job)
		c.JSON(http.StatusAccepted, gin.H{"job": job})
	}
}

// listServers names the git servers an importer can pick from.
func (a *API) listServers(c *gin.Context) {
	list, err := a.Servers.List(c.Request.Context())
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	out := make([]gin.H, len(list))
	for i, s := range list {
		out[i] = gin.H{"id": s.ID, "name": s.Name, "kind": s.Kind, "baseUrl": s.BaseURL}
	}
	c.JSON(http.StatusOK, out)
}

// job returns a job of a repository the user may read.
func (a *API) job(c *gin.Context) {
	id, ok := httpx.PathID(c)
	if !ok {
		return
	}
	j, err := a.Queue.Get(c.Request.Context(), id)
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	if httpx.Readable(c, j.RepoID, jobs.ErrNotFound) {
		jobView(c, &j)
		c.JSON(http.StatusOK, j)
	}
}

// jobView hides a job's error from users who cannot open the job console:
// it may quote git, the model's API or paths on the server.
func jobView(c *gin.Context, j *jobs.Job) {
	if j != nil && j.Error != "" && !httpx.Principal(c).Can(role.JobsView) {
		j.Error = "an administrator can find the details in the job console"
	}
}
