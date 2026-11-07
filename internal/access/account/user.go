// Package account keeps the users who sign in to deepcode: their passwords,
// roles and whether they are enabled. At least one enabled user always holds
// the admin role.
package account

import (
	"errors"
	"regexp"

	"deepcode/internal/access/account/store"
)

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = store.ErrNotFound
	ErrInvalid  = errors.New("無效的使用者")
	ErrConflict = errors.New("使用者衝突") // a taken username, or the last admin
)

type failure struct {
	kind error
	msg  string
}

func (f failure) Error() string        { return f.msg }
func (f failure) Is(target error) bool { return target == f.kind }

type (
	RoleRef = store.RoleRef
	User    = store.User
)

// Input creates or changes a user; nil fields keep their value. The username
// is set once, at creation.
type Input struct {
	Username    *string  `json:"username"`
	DisplayName *string  `json:"displayName"`
	Password    *string  `json:"password"`
	Disabled    *bool    `json:"disabled"`
	RoleIDs     *[]int64 `json:"roleIds"`
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,31}$`)

// Service applies the account rules over the users in Store.
type Service struct{ Store *store.Store }
