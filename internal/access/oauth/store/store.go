// Package store is the oauth package's persistence: the apps that registered,
// their authorization codes and refresh tokens, kept by their secret's hash.
// Package oauth holds the rules.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound: no app registered with the id.
var ErrNotFound = errors.New("找不到這個應用程式")

// ErrNoGrant: the code or refresh token is unknown, used or expired, or its
// user is disabled.
var ErrNoGrant = errors.New("授權碼或更新權杖無效、已使用或已過期")

type Store struct{ DB *sql.DB }

// Client is an app that registered.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

// App is an app a user lets act for them, as they see it.
type App struct {
	ClientID     string  `json:"clientId"`
	Name         string  `json:"name"`
	AuthorizedAt string  `json:"authorizedAt"`
	LastUsedAt   *string `json:"lastUsedAt"`
}

// Code is what an authorization code was given for.
type Code struct {
	ClientID    string
	UserID      int64
	RedirectURI string
	Challenge   string
}

// Grant is who a refresh token was given to, and since when.
type Grant struct {
	UserID int64
	Since  string // when the user approved the app: refreshing keeps it
}

func ttl(d time.Duration) string { return fmt.Sprintf("+%d seconds", int(d.Seconds())) }

// AddClient stores an app.
func (s *Store) AddClient(ctx context.Context, c Client) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO oauth_clients(id, name, redirect_uris) VALUES (?, ?, ?)`, c.ID, c.Name, string(uris))
	return err
}

// Client returns app id, or ErrNotFound.
func (s *Store) Client(ctx context.Context, id string) (Client, error) {
	c := Client{ID: id}
	var uris string
	err := s.DB.QueryRowContext(ctx, `SELECT name, redirect_uris FROM oauth_clients WHERE id = ?`, id).Scan(&c.Name, &uris)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal([]byte(uris), &c.RedirectURIs)
}

// unused matches the apps no user approved, or none does any longer.
const unused = `NOT EXISTS (SELECT 1 FROM oauth_refresh_tokens r WHERE r.client_id = c.id)
	AND NOT EXISTS (SELECT 1 FROM oauth_codes o WHERE o.client_id = c.id)
	AND NOT EXISTS (SELECT 1 FROM api_tokens t WHERE t.client_id = c.id)`

// Sweep deletes expired codes and refresh tokens, then the unused apps but
// the newest keep. An unused app is kept otherwise, however old: an MCP
// client registers once and comes back with the same id, even after its
// user declined, revoked it or let it lapse.
func (s *Store) Sweep(ctx context.Context, keep int) error {
	for _, q := range []string{
		`DELETE FROM oauth_codes WHERE expires_at <= CURRENT_TIMESTAMP`,
		`DELETE FROM oauth_refresh_tokens WHERE expires_at <= CURRENT_TIMESTAMP`,
	} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM oauth_clients WHERE id IN (SELECT id FROM oauth_clients AS c WHERE `+unused+`
		ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, keep)
	return err
}

// AddCode stores an authorization code lasting d.
func (s *Store) AddCode(ctx context.Context, hash string, c Code, d time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_codes(code_hash, client_id, user_id, redirect_uri, challenge, expires_at)
		VALUES (?, ?, ?, ?, ?, datetime('now', ?))`, hash, c.ClientID, c.UserID, c.RedirectURI, c.Challenge, ttl(d))
	return err
}

// TakeCode deletes the unexpired code of hash and returns what it was given
// for, or ErrNoGrant: a code works once.
func (s *Store) TakeCode(ctx context.Context, hash string) (Code, error) {
	var c Code
	err := s.DB.QueryRowContext(ctx, `DELETE FROM oauth_codes WHERE code_hash = ? AND expires_at > CURRENT_TIMESTAMP
		RETURNING client_id, user_id, redirect_uri, challenge`, hash).Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.Challenge)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoGrant
	}
	return c, err
}

// AddRefresh stores a refresh token of app clientID for user id, lasting d;
// since "" means now.
func (s *Store) AddRefresh(ctx context.Context, hash, clientID string, userID int64, since string, d time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash, client_id, user_id, created_at, expires_at)
		VALUES (?, ?, ?, COALESCE(NULLIF(?, ''), CURRENT_TIMESTAMP), datetime('now', ?))`, hash, clientID, userID, since, ttl(d))
	return err
}

// TakeRefresh deletes app clientID's unexpired refresh token of hash, held by
// an enabled user, and returns its grant, or ErrNoGrant: each works once.
func (s *Store) TakeRefresh(ctx context.Context, hash, clientID string) (Grant, error) {
	var g Grant
	err := s.DB.QueryRowContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token_hash = ? AND client_id = ?
		AND expires_at > CURRENT_TIMESTAMP AND user_id IN (SELECT id FROM users WHERE disabled = 0)
		RETURNING user_id, created_at`, hash, clientID).Scan(&g.UserID, &g.Since)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoGrant
	}
	return g, err
}

// Apps lists the apps user id lets act for them (holding an unexpired refresh
// token), the most recently approved first.
func (s *Store) Apps(ctx context.Context, userID int64) ([]App, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id, c.name, min(r.created_at),
		(SELECT max(t.last_used_at) FROM api_tokens t WHERE t.client_id = c.id AND t.user_id = ?1)
		FROM oauth_refresh_tokens r JOIN oauth_clients c ON c.id = r.client_id
		WHERE r.user_id = ?1 AND r.expires_at > CURRENT_TIMESTAMP GROUP BY c.id ORDER BY 3 DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.ClientID, &a.Name, &a.AuthorizedAt, &a.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Revoke deletes what user id gave app clientID: its codes, refresh and
// access tokens.
func (s *Store) Revoke(ctx context.Context, userID int64, clientID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"oauth_codes", "oauth_refresh_tokens", "api_tokens"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id = ? AND client_id = ?`, userID, clientID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
