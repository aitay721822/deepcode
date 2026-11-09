package authapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app/server/httpx"
)

// tokens lists the user's personal access tokens, never their secrets.
func (a *API) tokens(c *gin.Context) {
	list, err := a.Tokens.List(c.Request.Context(), httpx.Principal(c).UserID)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// createToken gives the user a personal access token; its secret is in this
// answer only.
func (a *API) createToken(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
		Days int    `json:"days"` // 0: until revoked
	}
	if !httpx.Bind(c, &body) {
		return
	}
	secret, t, err := a.Tokens.Create(c.Request.Context(), httpx.Principal(c).UserID, body.Name, body.Days)
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"token": t, "secret": secret})
}

func (a *API) revokeToken(c *gin.Context) {
	id, ok := httpx.PathID(c)
	if !ok {
		return
	}
	if err := a.Tokens.Revoke(c.Request.Context(), httpx.Principal(c).UserID, id); err != nil {
		httpx.DomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// apps lists the apps the user lets act for them.
func (a *API) apps(c *gin.Context) {
	list, err := a.OAuth.Apps(c.Request.Context(), httpx.Principal(c).UserID)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// revokeApp stops an app acting for the user; one they never approved changes nothing.
func (a *API) revokeApp(c *gin.Context) {
	if err := a.OAuth.Revoke(c.Request.Context(), httpx.Principal(c).UserID, c.Param("client")); err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
