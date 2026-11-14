// Package adminapi serves the admin console: users, roles, git servers, the
// jobs with their live output, and the models' token usage (usageapi).
package adminapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/account"
	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/app/server/adminapi/usageapi"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/platform/ai/usage"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
)

// API serves the admin console: users, roles, git servers, jobs and usage,
// each behind its permission.
type API struct {
	Sessions *session.Service // the sign-up switch, and checking a long stream's viewer again
	Accounts *account.Service
	Roles    *role.Service
	Servers  *gitserver.Service
	Catalog  *catalog.Catalog
	Queue    *jobs.Queue
	Bus      *events.Bus
	Usage    *usage.Manager
}

// Mount adds the routes to r, whose requests are signed in; each section
// requires its own permission.
func (a *API) Mount(r gin.IRouter) {
	people := r.Group("", httpx.Require(role.UsersManage))
	people.GET("/users", a.listUsers)
	people.POST("/users", a.createUser)
	people.PATCH("/users/:id", a.updateUser)
	people.DELETE("/users/:id", a.deleteUser)
	people.PATCH("/signup", a.setSignUp)
	people.GET("/roles", a.listRoles)
	people.POST("/roles", a.createRole)
	people.PATCH("/roles/:id", a.updateRole)
	people.DELETE("/roles/:id", a.deleteRole)
	people.GET("/permissions", a.permissions)
	people.GET("/repositories", a.repositories)

	servers := r.Group("/servers", httpx.Require(role.ServersManage))
	servers.GET("", a.listServers)
	servers.POST("", a.createServer)
	servers.PATCH("/:id", a.updateServer)
	servers.DELETE("/:id", a.deleteServer)
	servers.POST("/:id/test", a.testServer)
	servers.GET("/:id/oauth", a.authorizeServer)
	r.GET("/oauth/callback", httpx.Require(role.ServersManage), a.oauthCallback)

	work := r.Group("/jobs", httpx.Require(role.JobsView))
	work.GET("", a.listJobs)
	work.GET("/events", a.jobEvents)
	work.GET("/:id", a.getJob)
	work.GET("/:id/output", a.jobOutput)
	usageapi.Mount(r, a.Usage)
}

// reply answers with v, or with the error.
func reply(c *gin.Context, status int, v any, err error) {
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	if v == nil {
		c.Status(status)
		return
	}
	c.JSON(status, v)
}

func (a *API) permissions(c *gin.Context) {
	c.JSON(http.StatusOK, role.Permissions)
}

// repositories lists every repository for the role editor.
func (a *API) repositories(c *gin.Context) {
	list, err := a.Catalog.List(c.Request.Context(), nil)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	out := make([]gin.H, len(list))
	for i, r := range list {
		updated := "" // when its published snapshot was indexed; never indexed: none
		if r.Snapshot != nil {
			updated = r.Snapshot.IndexedAt
		}
		out[i] = gin.H{"id": r.PublicID, "owner": r.Owner, "name": r.Name, "provider": r.Provider, "serverName": r.ServerName, "updatedAt": updated}
	}
	c.JSON(http.StatusOK, out)
}
