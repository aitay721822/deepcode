package authapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"deepcode/internal/access/oauth"
	"deepcode/internal/app/server/httpx"
)

// MountOAuth adds the endpoints apps call, outside /api and without a
// session: the server's metadata (RFC 8414), registration (RFC 7591) and the
// token endpoint. The authorization endpoint is the web page /oauth/authorize,
// which asks the signed-in user through consentApp and consent.
func (a *API) MountOAuth(r gin.IRouter) {
	r.GET("/.well-known/oauth-authorization-server", a.metadata)
	r.POST("/oauth/register", a.register)
	r.POST("/oauth/token", a.token)
}

// metadata tells an app where the endpoints are, at the address it used.
func (a *API) metadata(c *gin.Context) {
	base := httpx.BaseURL(c)
	c.Header("Access-Control-Allow-Origin", "*") // public, read by apps in a browser too
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

// register stores an app as a public client: whatever authentication it
// asks for, it gets none, and proves itself with PKCE instead.
func (a *API) register(c *gin.Context) {
	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_client_metadata", "error_description": "the body is not client metadata JSON"})
		return
	}
	cl, err := a.OAuth.Register(c.Request.Context(), body.ClientName, body.RedirectURIs)
	if err != nil {
		oauthError(c, err)
		return
	}
	c.JSON(http.StatusCreated, &oauthex.ClientRegistrationResponse{
		ClientRegistrationMetadata: oauthex.ClientRegistrationMetadata{RedirectURIs: cl.RedirectURIs, ClientName: cl.Name,
			TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}},
		ClientID: cl.ID, ClientIDIssuedAt: time.Now(),
	})
}

// token trades a code or a refresh token for tokens. The request is a form,
// as OAuth has it; the client is named in it, or by HTTP Basic without a
// secret. A resource parameter (RFC 8707) is not checked: the tokens work at
// this server's /mcp and nowhere else.
func (a *API) token(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	client := c.PostForm("client_id")
	if id, _, ok := c.Request.BasicAuth(); ok && client == "" {
		client = id
	}
	var t oauth.Tokens
	var err error
	switch c.PostForm("grant_type") {
	case "authorization_code":
		t, err = a.OAuth.Exchange(c.Request.Context(), client, c.PostForm("code"), c.PostForm("redirect_uri"), c.PostForm("code_verifier"))
	case "refresh_token":
		t, err = a.OAuth.Refresh(c.Request.Context(), client, c.PostForm("refresh_token"))
	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
		return
	}
	if err != nil {
		oauthError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"access_token": t.Access, "token_type": "Bearer", "expires_in": t.ExpiresIn, "refresh_token": t.Refresh})
}

// oauthError answers err as OAuth does, {"error", "error_description"}, or as
// an internal error.
func oauthError(c *gin.Context, err error) {
	var e *oauth.Error
	if errors.As(err, &e) {
		status := http.StatusBadRequest
		if e.Code == "invalid_client" {
			status = http.StatusUnauthorized // RFC 6749 section 5.2
		}
		c.AbortWithStatusJSON(status, gin.H{"error": e.Code, "error_description": e.Error()})
		return
	}
	httpx.InternalError(c, err)
}

// consentApp names the app asking, for the consent page, while its redirect
// URI is one it registered.
func (a *API) consentApp(c *gin.Context) {
	cl, err := a.OAuth.Client(c.Request.Context(), c.Query("client_id"), c.Query("redirect_uri"))
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": cl.Name})
}

// consent answers an authorization request the user approved or declined
// with where to send the browser back to.
func (a *API) consent(c *gin.Context) {
	var body struct {
		oauth.Request
		Approve bool `json:"approve"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	to, err := a.OAuth.Approve(c.Request.Context(), httpx.Principal(c).UserID, body.Request, body.Approve)
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"redirect": to})
}
