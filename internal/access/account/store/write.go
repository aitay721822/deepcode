package store

import (
	"context"
	"database/sql"

	rolestore "deepcode/internal/access/role/store"
)

// Create adds a user with the password hash and, when roles is not nil,
// those roles, and returns its id.
func (s *Store) Create(ctx context.Context, username, displayName, hash string, roles *[]int64) (int64, error) {
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO users(username, display_name, password_hash) VALUES (?, ?, ?) RETURNING id`,
			username, displayName, hash).Scan(&id); err != nil {
			return err
		}
		if roles != nil {
			return setRoles(ctx, tx, id, *roles)
		}
		return nil
	})
	return id, err
}

// Change is what Update writes. Only the fields that are not nil are written
// (nil keeps the column), so edits made meanwhile to the others stay.
type Change struct {
	DisplayName  *string
	Disabled     *bool
	PasswordHash *string
	RoleIDs      *[]int64
	SignOut      bool // end the user's sessions
}

// Update writes c to user id in one transaction, which fails with ErrNotFound
// when there is no such user and with ErrLastAdmin when no enabled user would
// hold the admin role.
func (s *Store) Update(ctx context.Context, id int64, c Change) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET display_name = coalesce(?, display_name),
			disabled = coalesce(?, disabled), password_hash = coalesce(?, password_hash) WHERE id = ?`, c.DisplayName, c.Disabled, c.PasswordHash, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		// Sessions belong to the session package, but are deleted here so
		// that the sign-out commits with the change that causes it.
		if c.SignOut {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
				return err
			}
		}
		if c.RoleIDs != nil {
			if err := setRoles(ctx, tx, id, *c.RoleIDs); err != nil {
				return err
			}
		}
		return keepAdmin(ctx, tx)
	})
}

// Delete removes user id, with the user's sessions and conversations, in a
// transaction that fails with ErrNotFound when there is no such user and with
// ErrLastAdmin when no enabled user would hold the admin role.
func (s *Store) Delete(ctx context.Context, id int64) error {
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

// ReplacePassword sets user id's password hash to next only while old is
// still their hash and they are enabled, and ends their sessions in the same
// transaction; otherwise it fails with ErrNotFound.
func (s *Store) ReplacePassword(ctx context.Context, id int64, old, next string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ? AND disabled = 0`, next, id, old)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
		return err
	})
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

// keepAdmin fails with ErrLastAdmin when no enabled user would hold the admin role.
func keepAdmin(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users u JOIN user_roles ur ON ur.user_id = u.id
		WHERE ur.role_id = ? AND u.disabled = 0`, rolestore.Admin).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}
