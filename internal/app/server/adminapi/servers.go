package adminapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app/server/httpx"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/provider"
)

func (a *API) listServers(c *gin.Context) {
	list, err := a.Servers.List(c.Request.Context())
	reply(c, http.StatusOK, list, err)
}

func (a *API) createServer(c *gin.Context) {
	var in gitserver.Input
	if !httpx.Bind(c, &in) {
		return
	}
	s, err := a.Servers.Create(c.Request.Context(), in)
	reply(c, http.StatusCreated, s, err)
}

func (a *API) updateServer(c *gin.Context) {
	id, ok := httpx.PathID(c)
	var in gitserver.Input
	if !ok || !httpx.Bind(c, &in) {
		return
	}
	s, err := a.Servers.Update(c.Request.Context(), id, in)
	reply(c, http.StatusOK, s, err)
}

func (a *API) deleteServer(c *gin.Context) {
	if id, ok := httpx.PathID(c); ok {
		reply(c, http.StatusNoContent, nil, a.Servers.Delete(c.Request.Context(), id))
	}
}

// testServer calls the server's API with its token. A failure to reach it, or
// a token this secret key cannot decrypt, is the answer, not an error.
func (a *API) testServer(c *gin.Context) {
	id, ok := httpx.PathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	s, err := a.Servers.Get(ctx, id)
	detail := ""
	switch {
	case errors.Is(err, gitserver.ErrSealed): // the answer is err itself
	case err != nil:
		httpx.DomainError(c, err)
		return
	default:
		detail, err = provider.Check(ctx, s)
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "detail": detail})
}
