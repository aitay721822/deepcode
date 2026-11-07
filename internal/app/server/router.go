// Package server builds the HTTP handler: the JSON API under /api/ on Gin,
// behind sessions and permission checks, and the web UI for every other path.
package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app/server/adminapi"
	"deepcode/internal/app/server/authapi"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/app/server/repoapi"
)

// Router builds the Gin engine: response headers and guards, the session,
// public routes (health and the auth routes that need no session), the
// signed-in API, a JSON 404 for unknown API paths, and ui for everything else.
// loopback makes it reject foreign Host headers. ClientIP, by which sign-ins
// and sign-ups are limited, is the peer address, or what X-Forwarded-For says
// when the peer is one of the proxies (addresses or CIDRs).
func Router(auth *authapi.API, repos *repoapi.API, admin *adminapi.API, health gin.H, ui http.Handler, loopback bool, proxies []string) (*gin.Engine, error) {
	r := gin.New()
	if err := r.SetTrustedProxies(proxies); err != nil {
		return nil, err
	}
	r.Use(gin.Recovery(), httpx.Headers, httpx.Guard(loopback))

	api := r.Group("/api", httpx.Session(auth.Sessions))
	api.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, health) })
	signedIn := api.Group("", httpx.SignedIn)
	auth.Mount(api, signedIn)
	repos.Mount(signedIn)
	admin.Mount(signedIn.Group("/admin"))

	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			httpx.Fail(c, http.StatusNotFound, "找不到")
			return
		}
		c.Status(http.StatusOK) // NoRoute starts at 404; the UI sets its own status
		ui.ServeHTTP(c.Writer, c.Request)
	})
	return r, nil
}
