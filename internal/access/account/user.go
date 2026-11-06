// Package account keeps the users who sign in to deepcode: their passwords,
// roles and whether they are enabled. At least one enabled user always holds
// the admin role.
package account

import (
	"database/sql"
	"errors"
	"regexp"
)

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = errors.New("user not found")
	ErrInvalid  = errors.New("invalid user")
	ErrConflict = errors.New("user conflict") // a taken username, or the last admin
)

type failure struct {
	kind error
	msg  string
}

func (f failure) Error() string        { return f.msg }
func (f failure) Is(target error) bool { return target == f.kind }

type RoleRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type User struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Disabled    bool      `json:"disabled"`
	Roles       []RoleRef `json:"roles"`
	CreatedAt   string    `json:"createdAt"`
}

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

type Store struct{ DB *sql.DB }
