package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app/server/adminapi"
	"deepcode/internal/app/server/authapi"
	"deepcode/internal/app/server/repoapi"
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
