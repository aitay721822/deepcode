package httpx

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Guard blocks DNS rebinding (a foreign Host while bound to loopback) and
// cross-site form posts: request bodies must be JSON, which a form cannot
// send without a CORS preflight. The OAuth token endpoint alone takes a form,
// as OAuth has it; it reads no cookie, so a form posted there acts for nobody.
func Guard(loopback bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if loopback && !loopbackHost(c.Request.Host) {
			Fail(c, http.StatusForbidden, "非預期的 Host 標頭")
			return
		}
		m := c.Request.Method
		if (m == http.MethodPost || m == http.MethodPatch) && c.Request.URL.Path != "/oauth/token" &&
			!strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			Fail(c, http.StatusUnsupportedMediaType, "Content-Type 必須是 application/json")
			return
		}
		c.Next()
	}
}

// csp lets a page run scripts, connect and load fonts only from this server,
// so a script slipped into rendered content does not run and the page fetches
// nothing from elsewhere, and no other site can frame the app. Styles may also
// be inline, since components add them at run time (the dialogs' scroll lock),
// and images data: URIs, like the favicon.
const csp = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// Headers adds the browser protections to every response, the guards'
// refusals included: the policy above, no MIME sniffing, and no referrer for
// other sites. An API, OAuth or MCP response holds what one account may see,
// or a secret, so no cache, the browser's or a proxy's, keeps it.
func Headers(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	if p := c.Request.URL.Path; strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/oauth/") || p == "/mcp" {
		h.Set("Cache-Control", "no-store")
	}
	c.Next()
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
