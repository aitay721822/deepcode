package adminapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/account"
	"deepcode/internal/app/server/httpx"
)

func (a *API) listUsers(c *gin.Context) {
	list, err := a.Accounts.List(c.Request.Context())
	reply(c, http.StatusOK, list, err)
}

func (a *API) createUser(c *gin.Context) {
	var in account.Input
	if !httpx.Bind(c, &in) {
		return
	}
	u, err := a.Accounts.Create(c.Request.Context(), in)
	reply(c, http.StatusCreated, u, err)
}

func (a *API) updateUser(c *gin.Context) {
	id, ok := httpx.PathID(c)
	var in account.Input
	if !ok || !httpx.Bind(c, &in) {
		return
	}
	u, err := a.Accounts.Update(c.Request.Context(), id, httpx.Principal(c).UserID, in)
	reply(c, http.StatusOK, u, err)
}

// setSignUp opens or closes sign-up: whether visitors may create their own account.
func (a *API) setSignUp(c *gin.Context) {
	var body struct {
		Open *bool `json:"open"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	if body.Open == nil {
		httpx.Fail(c, http.StatusBadRequest, `"open" (true or false) is required`)
		return
	}
	err := a.Sessions.SetSignUpOpen(c.Request.Context(), *body.Open)
	reply(c, http.StatusOK, gin.H{"open": *body.Open}, err)
}

func (a *API) deleteUser(c *gin.Context) {
	if id, ok := httpx.PathID(c); ok {
		reply(c, http.StatusNoContent, nil, a.Accounts.Delete(c.Request.Context(), id, httpx.Principal(c).UserID))
	}
}
