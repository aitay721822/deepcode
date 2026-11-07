// Package store keeps the git servers in git_servers, their tokens and OAuth
// secrets as sealed.
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Store runs the git servers' SQL.
type Store struct{ DB *sql.DB }

// Server is a row of git_servers as stored: Token, ClientSecret and Refresh
// are sealed, and APIURL holds only an override.
type Server struct {
	ID              int64
	Name            string
	Kind            string
	BaseURL         string
	APIURL          string
	Token           string
	CreatedAt       string
	RepositoryCount int // the repositories imported from it
	OAuth
}

// OAuth is the OAuth app a server is authorized with (no ClientID: none),
// and what the last authorization left besides the access token.
type OAuth struct {
	ClientID, ClientSecret string
	Refresh, Redirect      string
	Expires                string // RFC 3339 UTC; empty for never
}

var (
	ErrNotFound  = errors.New("找不到 Git 伺服器")
	ErrDuplicate = errors.New("duplicate git server") // another server has the name or base URL
)

const columns = `g.id, g.name, g.kind, g.base_url, g.api_url, g.token, g.created_at,
	(SELECT count(*) FROM repositories r WHERE r.git_server_id = g.id),
	g.oauth_client_id, g.oauth_client_secret, g.oauth_refresh_token, g.oauth_redirect_uri, g.token_expires_at`

// scan reads columns, after the values in first.
func scan(row interface{ Scan(...any) error }, first ...any) (Server, error) {
	var s Server
	err := row.Scan(append(first, &s.ID, &s.Name, &s.Kind, &s.BaseURL, &s.APIURL, &s.Token, &s.CreatedAt, &s.RepositoryCount,
		&s.ClientID, &s.ClientSecret, &s.Refresh, &s.Redirect, &s.Expires)...)
	return s, err
}

// List returns every server, oldest first.
func (s *Store) List(ctx context.Context) ([]Server, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+columns+` FROM git_servers g ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		srv, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// Get returns server id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Server, error) {
	srv, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM git_servers g WHERE g.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return srv, err
}

// Create adds srv and returns its id, or ErrDuplicate.
func (s *Store) Create(ctx context.Context, srv Server) (int64, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO git_servers(name, kind, base_url, api_url, token, oauth_client_id, oauth_client_secret)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`, srv.Name, srv.Kind, srv.BaseURL, srv.APIURL, srv.Token, srv.ClientID, srv.ClientSecret).Scan(&id); err != nil {
		return 0, duplicate(err)
	}
	return id, nil
}

// Update stores what change makes of server id as stored, in one
// transaction (BEGIN IMMEDIATE: it holds the write lock from its start, so no
// other edit lands between the read, made on another connection of the pool,
// and the write); an error from change stores nothing. It returns ErrNotFound or
// ErrDuplicate.
func (s *Store) Update(ctx context.Context, id int64, change func(Server) (Server, error)) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	was, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	now, err := change(was)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE git_servers SET name = ?, kind = ?, base_url = ?, api_url = ?, token = ?, oauth_client_id = ?,
		oauth_client_secret = ?, oauth_refresh_token = ?, oauth_redirect_uri = ?, token_expires_at = ? WHERE id = ?`,
		now.Name, now.Kind, now.BaseURL, now.APIURL, now.Token, now.ClientID, now.ClientSecret, now.Refresh, now.Redirect, now.Expires, id); err != nil {
		return duplicate(err)
	}
	return tx.Commit()
}

// Delete removes server id unless repositories come from it; deleted is
// false when it did not, or when there is no such server.
func (s *Store) Delete(ctx context.Context, id int64) (deleted bool, err error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM git_servers WHERE id = ?1 AND NOT EXISTS (SELECT 1 FROM repositories WHERE git_server_id = ?1)`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Remote returns the web URL of repository repoID and its server, or
// ErrNotFound when either is gone.
func (s *Store) Remote(ctx context.Context, repoID int64) (webURL string, srv Server, err error) {
	srv, err = scan(s.DB.QueryRowContext(ctx, `SELECT r.url, `+columns+` FROM repositories r JOIN git_servers g ON g.id = r.git_server_id WHERE r.id = ?`,
		repoID), &webURL)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Server{}, ErrNotFound
	}
	return webURL, srv, err
}

// SetTokens stores what an OAuth authorization or refresh of server was, as
// read before asking, granted: token and refresh sealed. A server changed
// since (its app, kind or address, or its token: cleared, or granted again,
// since every seal differs) keeps its own, and SetTokens returns ErrNotFound.
func (s *Store) SetTokens(ctx context.Context, was Server, token, refresh, redirect, expires string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE git_servers SET token = ?, oauth_refresh_token = ?, oauth_redirect_uri = ?, token_expires_at = ?
		WHERE id = ? AND oauth_client_id = ? AND oauth_client_id != '' AND kind = ? AND base_url = ? AND api_url = ? AND token = ?`,
		token, refresh, redirect, expires, was.ID, was.ClientID, was.Kind, was.BaseURL, was.APIURL, was.Token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// duplicate tells a name or base URL another server has from other errors.
func duplicate(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicate
	}
	return err
}
