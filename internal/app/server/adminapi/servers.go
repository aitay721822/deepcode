package adminapi

import (
	"cmp"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"

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

// testServer calls the server's API with its token. A failure to reach it or
// its OAuth token endpoint, or a token this secret key cannot decrypt, is the
// answer, not an error.
func (a *API) testServer(c *gin.Context) {
	id, ok := httpx.PathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	s, err := a.Servers.Get(ctx, id)
	detail := ""
	switch {
	case errors.Is(err, gitserver.ErrSealed), errors.Is(err, gitserver.ErrOutage): // the answer is err itself
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

// authorizeServer sends the admin's browser to the server's OAuth consent
// page, which sends it back to oauthCallback.
func (a *API) authorizeServer(c *gin.Context) {
	id, ok := httpx.PathID(c)
	if !ok {
		return
	}
	to, err := a.Servers.Authorize(c.Request.Context(), id, httpx.Principal(c).UserID, callbackURL(c))
	if err != nil {
		backToServers(c, err)
		return
	}
	c.Redirect(http.StatusFound, to)
}

// oauthCallback is where a consent page sends the admin back: the code is
// traded for the server's tokens, and the admin returns to the git servers.
func (a *API) oauthCallback(c *gin.Context) {
	user, state := httpx.Principal(c).UserID, c.Query("state")
	if e := c.Query("error"); e != "" { // declined, or the app is set up wrong
		err := a.Servers.Decline(user, state) // what a link without a live state says is not shown
		if err == nil {
			err = fmt.Errorf("%w：%s", errDeclined, clip(cmp.Or(c.Query("error_description"), e)))
		}
		backToServers(c, err)
		return
	}
	backToServers(c, a.Servers.Finish(c.Request.Context(), user, state, c.Query("code")))
}

var errDeclined = errors.New("授權未完成")

// backToServers returns the admin to the console's git servers with how the
// authorization went: oauth=ok, or what to tell them.
func backToServers(c *gin.Context, err error) {
	result := "ok"
	switch {
	case err == nil:
	case errors.Is(err, errDeclined) || errors.Is(err, gitserver.ErrInvalid) || errors.Is(err, gitserver.ErrNotFound) ||
		errors.Is(err, gitserver.ErrConflict) || errors.Is(err, gitserver.ErrSealed):
		result = err.Error()
	default:
		log.Printf("adminapi: oauth: %v", err)
		result = "授權失敗；管理員可在伺服器記錄檔中查看詳細資訊"
	}
	c.Redirect(http.StatusFound, "/admin/servers?oauth="+url.QueryEscape(result))
}

// callbackURL is where consent pages send the admin back: this server, as
// their browser reached it. It is the redirect URI to register with the app.
func callbackURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host + "/api/admin/oauth/callback"
}

// clip shortens what a consent page's redirect says, which anyone can write.
func clip(s string) string {
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}
