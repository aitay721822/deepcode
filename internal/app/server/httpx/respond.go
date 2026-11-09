// Package httpx holds what every API handler shares: error responses, JSON
// binding, the request guards, sessions and permission checks, and
// server-sent event streams.
package httpx

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/account"
	"deepcode/internal/access/oauth"
	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/access/token"
	"deepcode/internal/app/importer"
	"deepcode/internal/knowledge/chat"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
)

const maxBody = 64 << 10

// Fail ends the request with {"error": msg}.
func Fail(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// InternalError logs err and answers 500 without it, since it may quote SQL,
// paths or another server's answer. Once the client has gone, err is only the
// cancellation, and nothing is logged.
func InternalError(c *gin.Context, err error) {
	if c.Request.Context().Err() == nil {
		log.Printf("api: %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
	}
	Fail(c, http.StatusInternalServerError, "伺服器內部錯誤；管理員可在伺服器記錄檔中查看詳細資訊")
}

// statuses maps the domain packages' kinds of failure to HTTP statuses.
var statuses = []struct {
	kinds  []error
	status int
}{
	{[]error{catalog.ErrNotFound, jobs.ErrNotFound, chat.ErrNotFound, gitserver.ErrNotFound, role.ErrNotFound, account.ErrNotFound,
		token.ErrNotFound}, http.StatusNotFound},
	{[]error{catalog.ErrInvalid, gitserver.ErrInvalid, role.ErrInvalid, account.ErrInvalid, token.ErrInvalid, oauth.ErrInvalid},
		http.StatusBadRequest},
	{[]error{gitserver.ErrConflict, role.ErrConflict, account.ErrConflict, gitserver.ErrSealed}, http.StatusConflict},
	{[]error{account.ErrBadLogin, session.ErrNoSession}, http.StatusUnauthorized},
	{[]error{importer.ErrForbidden, session.ErrSignUpClosed}, http.StatusForbidden},
	{[]error{session.ErrThrottled, session.ErrSignUpLimit}, http.StatusTooManyRequests},
	{[]error{catalog.ErrTooLarge}, http.StatusRequestEntityTooLarge},
	{[]error{catalog.ErrUpstream, gitserver.ErrOutage}, http.StatusBadGateway},
}

// DomainError answers with the status that fits the kind of err.
func DomainError(c *gin.Context, err error) {
	for _, s := range statuses {
		for _, kind := range s.kinds {
			if errors.Is(err, kind) {
				Fail(c, s.status, err.Error())
				return
			}
		}
	}
	InternalError(c, err)
}

// PathID reads the :id route parameter.
func PathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, "無效的 ID")
		return 0, false
	}
	return id, true
}

// Bind decodes a JSON body of at most 64 KB into v.
func Bind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
	if err := c.ShouldBindJSON(v); err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			Fail(c, http.StatusRequestEntityTooLarge, "請求內容超過 64 KB")
		} else {
			Fail(c, http.StatusBadRequest, "無效的 JSON 請求內容")
		}
		return false
	}
	return true
}
