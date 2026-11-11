package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Branch returns the branch repository id follows, "" for its default one,
// and that default.
func (s *Store) Branch(ctx context.Context, id int64) (tracked, def string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT tracked_branch, default_branch FROM repositories WHERE id = ?`, id).Scan(&tracked, &def)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return tracked, def, err
}

// SetBranch sets the branch repository id follows; "" follows its default.
func (s *Store) SetBranch(ctx context.Context, id int64, branch string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE repositories SET tracked_branch = ? WHERE id = ?`, branch, id)
	if err != nil {
		return err
	}
	switch n, err := res.RowsAffected(); {
	case err != nil:
		return err
	case n == 0:
		return fmt.Errorf("%w: repository %d", ErrNotFound, id)
	}
	return nil
}
