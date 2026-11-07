package store

import (
	"context"
	"database/sql"
	"errors"

	rolestore "deepcode/internal/access/role/store"
)

// List returns every user by username.
func (s *Store) List(ctx context.Context) ([]User, error) {
	return s.users(ctx, "ORDER BY username")
}

func (s *Store) Get(ctx context.Context, id int64) (User, error) {
	return s.one(ctx, "WHERE id = ?", id)
}

// ByUsername returns the user called username, in any letter case.
func (s *Store) ByUsername(ctx context.Context, username string) (User, error) {
	return s.one(ctx, "WHERE username = ?", username) // the column ignores case
}

// FirstAdmin returns the id of the oldest user holding the admin role.
func (s *Store) FirstAdmin(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT min(user_id) FROM user_roles WHERE role_id = ?`, rolestore.Admin).Scan(&id); err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, ErrNotFound
	}
	return id.Int64, nil
}

// Count returns how many users there are.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// Credentials returns the id, password hash and disabled flag of the user
// called username, in any letter case, or ErrNotFound.
func (s *Store) Credentials(ctx context.Context, username string) (id int64, hash string, disabled bool, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT id, password_hash, disabled FROM users WHERE username = ?`, username).
		Scan(&id, &hash, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return id, hash, disabled, err
}

// PasswordHash returns user id's password hash, or ErrNotFound.
func (s *Store) PasswordHash(ctx context.Context, id int64) (string, error) {
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return hash, err
}

func (s *Store) one(ctx context.Context, tail string, args ...any) (User, error) {
	list, err := s.users(ctx, tail, args...)
	if err != nil {
		return User{}, err
	}
	if len(list) == 0 {
		return User{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) users(ctx context.Context, tail string, args ...any) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, username, display_name, disabled, created_at FROM users `+tail, args...)
	if err != nil {
		return nil, err
	}
	out := []User{}
	index := map[int64]int{}
	for rows.Next() {
		u := User{Roles: []RoleRef{}}
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Disabled, &u.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		index[u.ID] = len(out)
		out = append(out, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT ur.user_id, r.id, r.name FROM user_roles ur JOIN roles r ON r.id = ur.role_id ORDER BY r.builtin DESC, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var user int64
		var r RoleRef
		if err := rows.Scan(&user, &r.ID, &r.Name); err != nil {
			return nil, err
		}
		if i, ok := index[user]; ok {
			out[i].Roles = append(out[i].Roles, r)
		}
	}
	return out, rows.Err()
}
