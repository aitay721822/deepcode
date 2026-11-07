// Package store is the session package's persistence: sessions, kept by
// their token's hash, and the sign-up switch. Package session holds the rules.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNoSession: no enabled user holds an unexpired session with the token.
var ErrNoSession = errors.New("請先登入")

type Store struct{ DB *sql.DB }

// Sweep deletes the sessions that have expired.
func (s *Store) Sweep(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP`)
	return err
}

// Start stores a session for tokenHash, lasting ttl, for user id while they
// are enabled and their password hash is still checked; otherwise it fails
// with ErrNoSession.
func (s *Store) Start(ctx context.Context, tokenHash string, id int64, checked string, ttl time.Duration) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at)
		SELECT ?, id, datetime('now', ?) FROM users WHERE id = ? AND disabled = 0 AND password_hash = ?`,
		tokenHash, fmt.Sprintf("+%d seconds", int(ttl.Seconds())), id, checked)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSession
	}
	return nil
}

// User returns the enabled user holding the unexpired session of tokenHash,
// or ErrNoSession.
func (s *Store) User(ctx context.Context, tokenHash string) (id int64, username, displayName string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT u.id, u.username, u.display_name FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > CURRENT_TIMESTAMP AND u.disabled = 0`, tokenHash).Scan(&id, &username, &displayName)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoSession
	}
	return id, username, displayName, err
}

// End deletes the session of tokenHash.
func (s *Store) End(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// SignUpOpen reports whether visitors may create their own account.
func (s *Store) SignUpOpen(ctx context.Context) (bool, error) {
	var open bool
	err := s.DB.QueryRowContext(ctx, `SELECT open FROM signup`).Scan(&open)
	return open, err
}

// SetSignUpOpen opens or closes sign-up.
func (s *Store) SetSignUpOpen(ctx context.Context, open bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE signup SET open = ?`, open)
	return err
}
