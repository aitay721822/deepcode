// Package repoapi serves the repositories a signed-in user may see: the
// catalog, code browsing and search, the wiki and the assistant. A job's
// error reaches only users with jobs.view in full (jobView).
package repoapi

import (
	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/app/importer"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/knowledge/chat"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
)

// API maps HTTP onto the domain packages: it binds requests, checks the
// user's permissions, calls them and turns their results into responses.
type API struct {
	Sessions  *session.Store // to check the asker of a long answer again
	Catalog   *catalog.Catalog
	Importer  *importer.Service
	Servers   *gitserver.Store
	Queue     *jobs.Queue
	Retriever *retrieval.Retriever
	Chat      *chat.Service
	Wiki      wiki.Reader
}

// Mount adds the routes to r, whose requests are signed in.
func (a *API) Mount(r gin.IRouter) {
	read := httpx.OnRepo(a.Catalog.ID, role.RepoRead)
	r.GET("/repos", a.listRepos)
	r.POST("/repos", httpx.Require(role.RepoImport), a.importRepo)
	r.GET("/servers", httpx.Require(role.RepoImport), a.listServers)
	r.GET("/repos/:id", read, a.getRepo)
	r.POST("/repos/:id/sync", httpx.OnRepo(a.Catalog.ID, role.RepoSync), a.syncRepo)
	r.GET("/jobs/:id", a.job)

	r.GET("/repos/:id/files", read, a.listFiles)
	r.GET("/repos/:id/files/*path", read, a.getFile)
	r.GET("/repos/:id/symbols", read, a.symbols)
	r.GET("/repos/:id/search", read, a.search)

	r.GET("/repos/:id/wiki", read, a.wiki)
	r.POST("/chat/stream", a.chatStream)
	r.GET("/conversations/:id", a.conversation)
}
