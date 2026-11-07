package httpx

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
)

const (
	sessionCookie = "deepcode_session"
	principalKey  = "principal"
	repoKey       = "repo"
)

// Session makes the principal of a valid session cookie available to the
// handlers through Principal.
func Session(sessions *session.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token := SessionToken(c); token != "" {
			p, err := sessions.Authenticate(c.Request.Context(), token)
			switch {
			case err == nil:
				c.Set(principalKey, p)
			case !errors.Is(err, session.ErrNoSession):
				InternalError(c, err)
				return
			}
		}
		c.Next()
	}
}

// Principal is the signed-in user, or nil.
func Principal(c *gin.Context) *session.Principal {
	p, _ := c.Get(principalKey)
	v, _ := p.(*session.Principal)
	return v
}

// SignedIn answers 401 without a valid session.
func SignedIn(c *gin.Context) {
	if Principal(c) == nil {
		Fail(c, http.StatusUnauthorized, session.ErrNoSession.Error())
	}
}

// Require answers 403 unless one of the user's roles grants perm.
func Require(perm role.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if p := Principal(c); p == nil || !p.Can(perm) {
			Fail(c, http.StatusForbidden, "you do not have the "+string(perm)+" permission")
		}
	}
}

// OnRepo guards a route on repository :id, its public id, which lookup turns
// into the row id that RepoID hands the handler. An unknown id (a row id
// among them) and a repository the user may not read both get 404, so its
// existence stays hidden; one who may read but lacks perm gets 403.
func OnRepo(lookup func(context.Context, string) (int64, error), perm role.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := lookup(c.Request.Context(), c.Param("id"))
		if err != nil {
			DomainError(c, err)
			return
		}
		if Visible(c, perm, id) {
			c.Set(repoKey, id)
		}
	}
}

// RepoID is the row id of the repository OnRepo let through.
func RepoID(c *gin.Context) int64 { return c.GetInt64(repoKey) }

// Visible answers like OnRepo when the user may not use perm on repository
// id, and reports whether they may.
func Visible(c *gin.Context, perm role.Permission, id int64) bool {
	p := Principal(c)
	switch {
	case p == nil || !p.CanOn(role.RepoRead, id):
		Fail(c, http.StatusNotFound, "repository not found")
	case !p.CanOn(perm, id):
		Fail(c, http.StatusForbidden, "you do not have the "+string(perm)+" permission on this repository")
	default:
		return true
	}
	return false
}

// CanRead reports whether the user may read repository repoID.
func CanRead(c *gin.Context, repoID int64) bool {
	p := Principal(c)
	return p != nil && p.CanOn(role.RepoRead, repoID)
}

// Readable answers 404 with notFound unless the user may read repository
// repoID, and reports whether they may: what belongs to a hidden repository,
// such as its jobs and conversations, is hidden too.
func Readable(c *gin.Context, repoID int64, notFound error) bool {
	if CanRead(c, repoID) {
		return true
	}
	Fail(c, http.StatusNotFound, notFound.Error())
	return false
}

// SetSession hands the browser its session token: HttpOnly, so scripts
// cannot read it, and SameSite=Lax, so other sites cannot post with it.
func SetSession(c *gin.Context, token string, ttl time.Duration) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"})
}

// ClearSession removes the session cookie.
func ClearSession(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// SessionToken is the session cookie's value, or "".
func SessionToken(c *gin.Context) string {
	token, _ := c.Cookie(sessionCookie)
	return token
}
