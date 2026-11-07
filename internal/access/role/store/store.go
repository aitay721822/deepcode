// Package store is the role package's persistence: the SQL that reads and
// writes roles, their permissions and repositories, and what a user's roles
// grant. Package role holds the rules.
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Admin and Member are the ids the schema gives the built-in roles.
const (
	Admin  int64 = 1
	Member int64 = 2
)

// ErrNotFound: no role has the id.
var ErrNotFound = errors.New("找不到角色")

// Refusals of the database, which package role words for people.
var (
	ErrDuplicate    = errors.New("a role has this name") // a UNIQUE constraint
	ErrNoRepository = errors.New("no such repository")   // a FOREIGN KEY constraint, or an unknown public id
)

// Permission names one thing a user may do; package role lists them.
type Permission string

type Role struct {
	ID              int64        `json:"id"`
	Name            string       `json:"name"`
	Description     string       `json:"description"`
	Builtin         bool         `json:"builtin"`
	Permissions     []Permission `json:"permissions"`
	AllRepositories bool         `json:"allRepositories"`
	RepositoryIDs   []string     `json:"repositoryIds"` // public ids, when not AllRepositories
	UserCount       int          `json:"userCount"`
}

type Store struct{ DB *sql.DB }

// scanPairs calls add for every (role id, value) row of query.
func scanPairs[V any](ctx context.Context, db *sql.DB, query string, add func(int64, V), args ...any) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var v V
		if err := rows.Scan(&id, &v); err != nil {
			return err
		}
		add(id, v)
	}
	return rows.Err()
}

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
	case err == nil:
		return nil
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		return ErrDuplicate
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		return ErrNoRepository
	}
	return err
}
