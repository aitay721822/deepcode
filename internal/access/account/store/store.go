// Package store is the account package's persistence: the SQL that reads and
// writes users, their roles and password hashes, and that ends a user's
// sessions in the transaction that requires it. Package account holds the
// rules.
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrNotFound: no user matches.
var ErrNotFound = errors.New("找不到使用者")

// Refusals of the database, which package account words for people.
var (
	ErrTaken     = errors.New("the username is taken")         // a UNIQUE constraint
	ErrNoRole    = errors.New("no such role")                  // a FOREIGN KEY constraint
	ErrLastAdmin = errors.New("no enabled admin would remain") // see keepAdmin
)

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

type Store struct{ DB *sql.DB }

func (s *Store) write(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return explain(err)
	}
	return explain(tx.Commit())
}

// explain turns constraint failures into errors a caller can act on.
func explain(err error) error {
	switch {
	case err == nil, errors.Is(err, ErrNotFound):
		return err
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		return ErrTaken
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		return ErrNoRole
	}
	return err
}
