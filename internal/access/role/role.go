package role

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// Admin is the built-in role holding every permission on every repository.
const Admin int64 = 1

// Member is the built-in role for reading and asking about the repositories
// granted to it; visitors who sign themselves up get it.
const Member int64 = 2

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = errors.New("role not found")
	ErrInvalid  = errors.New("invalid role")
	ErrConflict = errors.New("role conflict") // a duplicate name, or a built-in role
)

type Role struct {
	ID              int64        `json:"id"`
	Name            string       `json:"name"`
	Description     string       `json:"description"`
	Builtin         bool         `json:"builtin"`
	Permissions     []Permission `json:"permissions"`
	AllRepositories bool         `json:"allRepositories"`
	RepositoryIDs   []int64      `json:"repositoryIds"` // when not AllRepositories
	UserCount       int          `json:"userCount"`
}

type Store struct{ DB *sql.DB }

// List returns every role by name, built-in ones first.
func (s *Store) List(ctx context.Context) ([]Role, error) {
	return s.roles(ctx, "ORDER BY builtin DESC, name")
}

func (s *Store) Get(ctx context.Context, id int64) (Role, error) {
	list, err := s.roles(ctx, "WHERE id = ?", id)
	if err != nil {
		return Role{}, err
	}
	if len(list) == 0 {
		return Role{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) roles(ctx context.Context, tail string, args ...any) ([]Role, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, description, builtin, all_repositories,
		(SELECT count(*) FROM user_roles u WHERE u.role_id = roles.id) FROM roles `+tail, args...)
	if err != nil {
		return nil, err
	}
	out := []Role{}
	index := map[int64]int{}
	for rows.Next() {
		r := Role{Permissions: []Permission{}, RepositoryIDs: []int64{}}
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Builtin, &r.AllRepositories, &r.UserCount); err != nil {
			rows.Close()
			return nil, err
		}
		index[r.ID] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// ponytail: both link tables are read whole; filter by role once there are thousands of roles.
	err = scanPairs(ctx, s.DB, `SELECT role_id, permission FROM role_permissions ORDER BY permission`, func(id int64, p Permission) {
		if i, ok := index[id]; ok {
			out[i].Permissions = append(out[i].Permissions, p)
		}
	})
	if err != nil {
		return nil, err
	}
	err = scanPairs(ctx, s.DB, `SELECT role_id, repository_id FROM role_repositories ORDER BY repository_id`, func(id, repo int64) {
		if i, ok := index[id]; ok {
			out[i].RepositoryIDs = append(out[i].RepositoryIDs, repo)
		}
	})
	for i := range out {
		sort.Slice(out[i].Permissions, func(a, b int) bool { return order(out[i].Permissions[a]) < order(out[i].Permissions[b]) })
	}
	return out, err
}

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

// order is a permission's place in Permissions.
func order(p Permission) int {
	for i, info := range Permissions {
		if info.Name == p {
			return i
		}
	}
	return len(Permissions)
}
