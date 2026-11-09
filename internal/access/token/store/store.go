// Package store is the token package's persistence: bearer tokens kept by
// their secret's hash. Package token holds the rules.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound: the user has no such personal token.
var ErrNotFound = errors.New("找不到這個權杖")

// ErrNoToken: no enabled user holds an unexpired token with the hash.
var ErrNoToken = errors.New("權杖無效或已過期")

type Store struct{ DB *sql.DB }

// Token is a personal access token as its owner sees it: never its secret.
type Token struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	CreatedAt  string  `json:"createdAt"`
	LastUsedAt *string `json:"lastUsedAt"`
	ExpiresAt  *string `json:"expiresAt"` // nil: until revoked
}

// Holder is who a token signs in.
type Holder struct {
	UserID      int64
	Username    string
	DisplayName string
	ExpiresAt   *string
}

// Add stores a token for user id while they are enabled, lasting ttl (0: until
// revoked), an app's when clientID is set; ErrNoToken when the user is gone
// or disabled.
func (s *Store) Add(ctx context.Context, userID int64, clientID, name, hash string, ttl time.Duration) (Token, error) {
	var t Token
	err := s.DB.QueryRowContext(ctx, `INSERT INTO api_tokens(user_id, client_id, name, token_hash, expires_at)
		SELECT id, NULLIF(?, ''), ?, ?, CASE WHEN ? > 0 THEN datetime('now', ?) END FROM users WHERE id = ? AND disabled = 0
		RETURNING id, name, created_at, last_used_at, expires_at`,
		clientID, name, hash, int(ttl.Seconds()), fmt.Sprintf("+%d seconds", int(ttl.Seconds())), userID).
		Scan(&t.ID, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoToken
	}
	return t, err
}

// Personal lists user id's unexpired personal tokens, newest first.
func (s *Store) Personal(ctx context.Context, userID int64) ([]Token, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, created_at, last_used_at, expires_at FROM api_tokens
		WHERE user_id = ? AND client_id IS NULL AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP) ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Delete removes user id's personal token id, or reports ErrNotFound.
func (s *Store) Delete(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ? AND client_id IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Holder returns the enabled user holding the unexpired token of hash, or
// ErrNoToken, and marks the token used, at most once a minute.
func (s *Store) Holder(ctx context.Context, hash string) (Holder, error) {
	var h Holder
	err := s.DB.QueryRowContext(ctx, `SELECT u.id, u.username, u.display_name, t.expires_at FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND (t.expires_at IS NULL OR t.expires_at > CURRENT_TIMESTAMP) AND u.disabled = 0`, hash).
		Scan(&h.UserID, &h.Username, &h.DisplayName, &h.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return h, ErrNoToken
	}
	if err != nil {
		return h, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP
		WHERE token_hash = ? AND (last_used_at IS NULL OR last_used_at < datetime('now', '-1 minute'))`, hash)
	return h, err
}

// Sweep deletes the tokens that have expired.
func (s *Store) Sweep(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE expires_at <= CURRENT_TIMESTAMP`)
	return err
}
