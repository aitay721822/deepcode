package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app/server/adminapi"
	"deepcode/internal/app/server/authapi"
	"deepcode/internal/app/server/repoapi"
	"deepcode/internal/app/server/repoapi/hooks"
)

// TestHeaders: pages carry the browser protections, and so do the guards'
// refusals (here of a foreign Host). The policy is spelled out, so loosening it
// takes a change here too.
func TestHeaders(t *testing.T) {
	const policy = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
		"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	gin.SetMode(gin.TestMode)
	r, err := Router(&authapi.API{}, &repoapi.API{}, &adminapi.API{}, gin.H{}, http.NotFoundHandler(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1:8080", "evil.example"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		h := w.Header()
		if h.Get("Content-Security-Policy") != policy || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s: %d %v", host, w.Code, h)
		}
		if h.Get("Cache-Control") == "no-store" {
			t.Errorf("%s: no-store outside the API", host)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
	req.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("an API response may be cached: %v", w.Header())
	}
}

// TestTrustedProxies: the client address, which sign-ins are limited by, is
// the peer's unless the peer is a trusted proxy; then X-Forwarded-For names it.
func TestTrustedProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := func(proxies []string, peer string) string {
		r, err := Router(&authapi.API{}, &repoapi.API{}, &adminapi.API{}, gin.H{}, http.NotFoundHandler(), false, proxies)
		if err != nil {
			t.Fatal(err)
		}
		r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
		req := httptest.NewRequest(http.MethodGet, "/ip", nil)
		req.RemoteAddr = peer + ":4000"
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Body.String()
	}
	if got := client(nil, "127.0.0.1"); got != "127.0.0.1" {
		t.Errorf("no proxies: %s", got)
	}
	if got := client([]string{"127.0.0.1", "10.0.0.0/8"}, "10.1.2.3"); got != "203.0.113.9" {
		t.Errorf("from a trusted proxy: %s", got)
	}
	if got := client([]string{"10.0.0.0/8"}, "192.0.2.1"); got != "192.0.2.1" {
		t.Errorf("from another peer: %s", got)
	}
	if _, err := Router(&authapi.API{}, &repoapi.API{}, &adminapi.API{}, gin.H{}, http.NotFoundHandler(), false, []string{"proxy.local"}); err == nil {
		t.Error("accepted a proxy that is no address")
	}
}

// frozen are the routes v1.0.0 serves, and v2.0.0's GET /api/admin/model
// (README's compatibility promise): a release may add routes, never drop or
// rename one of these. /mcp counts by the methods its transport answers.
const frozen = `
	GET /.well-known/oauth-authorization-server, GET /.well-known/oauth-protected-resource
	GET /.well-known/oauth-protected-resource/mcp, GET /api/admin/jobs, GET /api/admin/jobs/:id
	GET /api/admin/jobs/:id/output, GET /api/admin/jobs/events, GET /api/admin/oauth/callback
	GET /api/admin/permissions, GET /api/admin/repositories, GET /api/admin/roles
	POST /api/admin/roles, DELETE /api/admin/roles/:id, PATCH /api/admin/roles/:id
	GET /api/admin/servers, POST /api/admin/servers, DELETE /api/admin/servers/:id
	PATCH /api/admin/servers/:id, GET /api/admin/servers/:id/oauth, POST /api/admin/servers/:id/test
	PATCH /api/admin/signup, GET /api/admin/usage, GET /api/admin/users, POST /api/admin/users
	DELETE /api/admin/users/:id, PATCH /api/admin/users/:id, GET /api/auth/apps
	DELETE /api/auth/apps/:client, POST /api/auth/login, POST /api/auth/logout, GET /api/auth/me
	POST /api/auth/password, GET /api/auth/signup, POST /api/auth/signup, GET /api/auth/tokens
	POST /api/auth/tokens, DELETE /api/auth/tokens/:id, POST /api/chat/stream
	GET /api/conversations/:id, GET /api/health, POST /api/hooks/:id, GET /api/jobs/:id
	POST /api/jobs/:id/stop, GET /api/oauth/authorize, POST /api/oauth/authorize, GET /api/repos
	POST /api/repos, GET /api/repos/:id, POST /api/repos/:id/branch, GET /api/repos/:id/branches
	GET /api/repos/:id/files, GET /api/repos/:id/files/*path, GET /api/repos/:id/search
	GET /api/repos/:id/symbols, POST /api/repos/:id/sync, DELETE /api/repos/:id/webhook
	GET /api/repos/:id/webhook, POST /api/repos/:id/webhook, GET /api/repos/:id/wiki
	GET /api/repos/:id/wiki/export, GET /api/servers, DELETE /mcp, GET /mcp, POST /mcp
	POST /oauth/register, POST /oauth/token, GET /api/admin/model
`

// TestFrozenRoutes: every route of v1.0.0 and v2.0.0 is still served, the webhook's among them.
func TestFrozenRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, err := Router(&authapi.API{}, &repoapi.API{Webhooks: &hooks.API{}}, &adminapi.API{}, gin.H{}, http.NotFoundHandler(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	served := map[string]bool{}
	for _, route := range r.Routes() {
		served[route.Method+" "+route.Path] = true
	}
	for _, route := range strings.FieldsFunc(frozen, func(r rune) bool { return r == ',' || r == '\n' }) {
		if route = strings.TrimSpace(route); !served[route] {
			t.Errorf("released route %s is gone", route)
		}
	}
}
