package account

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"deepcode/internal/access/role"
)

// List returns every user by username.
func (s *Store) List(ctx context.Context) ([]User, error) {
	return s.users(ctx, "ORDER BY username")
}

func (s *Store) Get(ctx context.Context, id int64) (User, error) {
	return s.one(ctx, "WHERE id = ?", id)
}

// FirstAdmin returns the id of the oldest user holding the admin role.
func (s *Store) FirstAdmin(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT min(user_id) FROM user_roles WHERE role_id = ?`, role.Admin).Scan(&id); err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, ErrNotFound
	}
	return id.Int64, nil
}

// ByUsername returns the user called username, in any letter case.
func (s *Store) ByUsername(ctx context.Context, username string) (User, error) {
	return s.one(ctx, "WHERE username = ?", strings.TrimSpace(username)) // the column ignores case
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

func (s *Store) Create(ctx context.Context, in Input) (User, error) {
	if in.Username == nil || !usernameRe.MatchString(strings.TrimSpace(*in.Username)) {
		return User{}, failure{ErrInvalid, "a username of 2 to 32 letters, digits, '.', '_' or '-', starting with a letter or digit, is required"}
	}
	name := strings.TrimSpace(*in.Username)
	display := name
	if in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) != "" {
		display = strings.TrimSpace(*in.DisplayName)
	}
	if in.Password == nil {
		return User{}, failure{ErrInvalid, "a password is required"}
	}
	if err := errors.Join(checkDisplay(display), checkPassword(*in.Password)); err != nil {
		return User{}, err
	}
	encoded, err := hash(*in.Password)
	if err != nil {
		return User{}, err
	}
	var id int64
	err = s.write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO users(username, display_name, password_hash) VALUES (?, ?, ?) RETURNING id`,
			name, display, encoded).Scan(&id); err != nil {
			return err
		}
		if in.RoleIDs != nil {
			return setRoles(ctx, tx, id, *in.RoleIDs)
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, id)
}

// Update changes user id on behalf of actor, the signed-in user. A new
// password or disabling the user signs them out everywhere.
func (s *Store) Update(ctx context.Context, id, actor int64, in Input) (User, error) {
	if in.Disabled != nil && *in.Disabled && id == actor {
		return User{}, failure{ErrConflict, "you cannot disable yourself"}
	}
	// Only the fields in sets are written (nil keeps the column), so edits
	// made meanwhile to the others stay.
	var display, disabled, encoded any
	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if err := checkDisplay(name); err != nil {
			return User{}, err
		}
		display = name
	}
	if in.Disabled != nil {
		disabled = *in.Disabled
	}
	if in.Password != nil {
		if err := checkPassword(*in.Password); err != nil {
			return User{}, err
		}
		h, err := hash(*in.Password)
		if err != nil {
			return User{}, err
		}
		encoded = h
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET display_name = coalesce(?, display_name),
			disabled = coalesce(?, disabled), password_hash = coalesce(?, password_hash) WHERE id = ?`, display, disabled, encoded, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		// Sessions belong to the session package, but are deleted here so
		// that the sign-out commits with the change that causes it.
		if encoded != nil || in.Disabled != nil && *in.Disabled {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
				return err
			}
		}
		if in.RoleIDs != nil {
			if err := setRoles(ctx, tx, id, *in.RoleIDs); err != nil {
				return err
			}
		}
		return keepAdmin(ctx, tx)
	})
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, id)
}

// Delete removes user id on behalf of actor, with the user's sessions and conversations.
func (s *Store) Delete(ctx context.Context, id, actor int64) error {
	if id == actor {
		return failure{ErrConflict, "you cannot delete yourself"}
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return keepAdmin(ctx, tx)
	})
}

// checkDisplay wants 1 to 64 characters (not bytes), none of them a control
// character or a line break, which would break the line a name is shown on,
// or a bidi control, which would reorder the text around it. Other invisible
// characters stay: Persian and Indic names and emoji sequences need joiners.
func checkDisplay(name string) error {
	disruptive := strings.ContainsFunc(name, func(r rune) bool {
		return unicode.In(r, unicode.Cc, unicode.Zl, unicode.Zp, unicode.Bidi_Control)
	})
	if n := utf8.RuneCountInString(name); n == 0 || n > 64 || disruptive {
		return failure{ErrInvalid, "a display name of 1 to 64 characters, without control characters, line breaks or text-direction marks, is required"}
	}
	return nil
}

func setRoles(ctx context.Context, tx *sql.Tx, user int64, roles []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, user); err != nil {
		return err
	}
	for _, r := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_roles(user_id, role_id) VALUES (?, ?)`, user, r); err != nil {
			return err
		}
	}
	return nil
}

// keepAdmin fails when no enabled user would hold the admin role.
func keepAdmin(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users u JOIN user_roles ur ON ur.user_id = u.id
		WHERE ur.role_id = ? AND u.disabled = 0`, role.Admin).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return failure{ErrConflict, "at least one enabled user must keep the admin role"}
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
	case err == nil, errors.Is(err, ErrNotFound):
		return err
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		return failure{ErrConflict, "this username is taken"}
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		return failure{ErrInvalid, "unknown role"}
	}
	return err
}
