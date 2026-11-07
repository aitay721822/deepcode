// Package authapi signs users in and out, signs visitors up while sign-up is
// open, and serves their own account.
package authapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/app/server/httpx"
)

// API serves /auth: sign-in, sign-up, sign-out and the signed-in user's own account.
type API struct{ Sessions *session.Service }

// Mount adds sign-in, sign-up and sign-out to public and the account routes to signedIn.
func (a *API) Mount(public, signedIn gin.IRouter) {
	public.POST("/auth/login", a.login)
	public.GET("/auth/signup", a.signUpOpen)
	public.POST("/auth/signup", a.signUp)
	public.POST("/auth/logout", a.logout)
	signedIn.GET("/auth/me", a.me)
	signedIn.POST("/auth/password", a.password)
}

// me is the signed-in user as the UI sees them.
type me struct {
	ID              int64             `json:"id"`
	Username        string            `json:"username"`
	DisplayName     string            `json:"displayName"`
	Permissions     []role.Permission `json:"permissions"`
	AllRepositories bool              `json:"allRepositories"`
}

func view(p *session.Principal) me {
	return me{p.UserID, p.Username, p.DisplayName, p.Permissions(), p.AllRepositories()}
}

func (a *API) login(c *gin.Context) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	token, p, err := a.Sessions.Login(c.Request.Context(), body.Username, body.Password, c.ClientIP())
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	httpx.SetSession(c, token, a.Sessions.TTL)
	c.JSON(http.StatusOK, view(p))
}

// signUpOpen tells the sign-in page whether to offer sign-up.
func (a *API) signUpOpen(c *gin.Context) {
	open, err := a.Sessions.SignUpOpen(c.Request.Context())
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"open": open})
}

// signUp creates an account with the member role and signs this browser in.
func (a *API) signUp(c *gin.Context) {
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	token, p, err := a.Sessions.SignUp(c.Request.Context(), body.Username, body.DisplayName, body.Password, c.ClientIP())
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	httpx.SetSession(c, token, a.Sessions.TTL)
	c.JSON(http.StatusCreated, view(p))
}

func (a *API) logout(c *gin.Context) {
	if token := httpx.SessionToken(c); token != "" {
		if err := a.Sessions.Logout(c.Request.Context(), token); err != nil {
			httpx.InternalError(c, err)
			return
		}
	}
	httpx.ClearSession(c)
	c.Status(http.StatusNoContent)
}

func (a *API) me(c *gin.Context) {
	c.JSON(http.StatusOK, view(httpx.Principal(c)))
}

// password changes the user's own password; this browser gets the new session.
func (a *API) password(c *gin.Context) {
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	token, err := a.Sessions.ChangePassword(c.Request.Context(), httpx.Principal(c).UserID, body.Current, body.Next)
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	httpx.SetSession(c, token, a.Sessions.TTL)
	c.Status(http.StatusNoContent)
}
