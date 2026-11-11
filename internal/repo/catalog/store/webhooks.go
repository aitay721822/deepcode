package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// WebhookSecret returns the sealed secret of repository id, "" when it has
// none, and ErrNotFound for no such repository.
func (s *Store) WebhookSecret(ctx context.Context, id int64) (string, error) {
	var secret string
	err := s.DB.QueryRowContext(ctx, `SELECT webhook_secret FROM repositories WHERE id = ?`, id).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return secret, err
}

// SetWebhookSecret sets the sealed secret of repository id; "" clears it.
func (s *Store) SetWebhookSecret(ctx context.Context, id int64, sealed string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE repositories SET webhook_secret = ? WHERE id = ?`, sealed, id)
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
