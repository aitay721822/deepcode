package role

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Input creates or changes a role; nil fields keep their value.
type Input struct {
	Name            *string       `json:"name"`
	Description     *string       `json:"description"`
	Permissions     *[]Permission `json:"permissions"`
	AllRepositories *bool         `json:"allRepositories"`
	RepositoryIDs   *[]int64      `json:"repositoryIds"`
}

type failure struct {
	kind error
	msg  string
}

func (f failure) Error() string        { return f.msg }
func (f failure) Is(target error) bool { return target == f.kind }

func (s *Store) Create(ctx context.Context, in Input) (Role, error) {
	var r Role
	if err := fill(&r, in); err != nil {
		return Role{}, err
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO roles(name, description, all_repositories) VALUES (?, ?, ?) RETURNING id`,
			r.Name, r.Description, r.AllRepositories).Scan(&r.ID); err != nil {
			return err
		}
		return links(ctx, tx, r, in)
	})
	if err != nil {
		return Role{}, err
	}
	return s.Get(ctx, r.ID)
}

// Update changes role id. Built-in roles keep their names, and the admin role
// keeps every permission on every repository.
func (s *Store) Update(ctx context.Context, id int64, in Input) (Role, error) {
	err := s.write(ctx, func(tx *sql.Tx) error {
		// Read while this transaction holds the write lock (BEGIN IMMEDIATE),
		// so no other edit lands between the read and the write.
		r, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		if r.Builtin && in.Name != nil && strings.TrimSpace(*in.Name) != r.Name {
			return failure{ErrConflict, "built-in roles cannot be renamed"}
		}
		if id == Admin && !unchangedAdmin(in) {
			return failure{ErrConflict, "the admin role always holds every permission on every repository"}
		}
		if err := fill(&r, in); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE roles SET name = ?, description = ?, all_repositories = ? WHERE id = ?`,
			r.Name, r.Description, r.AllRepositories, id); err != nil {
			return err
		}
		return links(ctx, tx, r, in)
	})
	if err != nil {
		return Role{}, err
	}
	return s.Get(ctx, id)
}

// unchangedAdmin reports whether in keeps the admin role's grants as they
// are, as the admin console does when it sends the whole role back.
func unchangedAdmin(in Input) bool {
	if in.AllRepositories != nil && !*in.AllRepositories || in.RepositoryIDs != nil && len(*in.RepositoryIDs) > 0 {
		return false
	}
	if in.Permissions == nil {
		return true
	}
	have := map[Permission]bool{}
	for _, p := range *in.Permissions {
		have[p] = true
	}
	for _, i := range Permissions {
		if !have[i.Name] {
			return false
		}
	}
	return true
}

// Delete removes role id; built-in roles stay.
func (s *Store) Delete(ctx context.Context, id int64) error {
	r, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if r.Builtin {
		return failure{ErrConflict, "built-in roles cannot be deleted"}
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id)
	return err
}

// fill validates in and applies its fields to r.
func fill(r *Role, in Input) error {
	if in.Name != nil {
		r.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		r.Description = strings.TrimSpace(*in.Description)
	}
	if in.AllRepositories != nil {
		r.AllRepositories = *in.AllRepositories
	}
	switch {
	case r.Name == "" || utf8.RuneCountInString(r.Name) > 64: // characters, not bytes
		return failure{ErrInvalid, "a name of 1 to 64 characters is required"}
	case utf8.RuneCountInString(r.Description) > 500:
		return failure{ErrInvalid, "the description is longer than 500 characters"}
	}
	if in.Permissions != nil {
		for _, p := range *in.Permissions {
			if !known(p) {
				return failure{ErrInvalid, fmt.Sprintf("unknown permission %q", p)}
			}
		}
	}
	return nil
}

// links replaces the permissions and repositories of r that in sets; a role
// on every repository lists none.
func links(ctx context.Context, tx *sql.Tx, r Role, in Input) error {
	if in.Permissions != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, r.ID); err != nil {
			return err
		}
		for _, p := range *in.Permissions {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO role_permissions(role_id, permission) VALUES (?, ?)`, r.ID, p); err != nil {
				return err
			}
		}
	}
	if in.RepositoryIDs == nil && !r.AllRepositories {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_repositories WHERE role_id = ?`, r.ID); err != nil {
		return err
	}
	if r.AllRepositories {
		return nil
	}
	for _, repo := range *in.RepositoryIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO role_repositories(role_id, repository_id) VALUES (?, ?)`, r.ID, repo); err != nil {
			return err
		}
	}
	return nil
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
		return failure{ErrConflict, "a role with this name already exists"}
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		return failure{ErrInvalid, "unknown repository"}
	}
	return err
}
