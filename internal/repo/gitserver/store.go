package gitserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"deepcode/internal/repo/git"
)

// Store keeps the servers in git_servers, tokens sealed by Box.
type Store struct {
	DB  *sql.DB
	Box *Box
}

const columns = `g.id, g.name, g.kind, g.base_url, g.api_url, g.token, g.created_at,
	(SELECT count(*) FROM repositories r WHERE r.git_server_id = g.id)`

// scan reads a row as stored: APIURL holds only an override.
func scan(row interface{ Scan(...any) error }) (Server, error) {
	var s Server
	err := row.Scan(&s.ID, &s.Name, &s.Kind, &s.BaseURL, &s.APIURL, &s.sealed, &s.CreatedAt, &s.RepositoryCount)
	s.HasToken = s.sealed != ""
	return s, err
}

// effective fills in the kind's default API URL.
func (s Server) effective() Server {
	if s.APIURL == "" {
		s.APIURL = defaultAPI(s.Kind, s.BaseURL)
	}
	return s
}

// List returns every server, oldest first; tokens stay sealed.
func (st *Store) List(ctx context.Context) ([]Server, error) {
	rows, err := st.DB.QueryContext(ctx, `SELECT `+columns+` FROM git_servers g ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s.effective())
	}
	return out, rows.Err()
}

// Get returns server id with its token unsealed.
func (st *Store) Get(ctx context.Context, id int64) (Server, error) {
	s, err := st.stored(ctx, id)
	if err != nil {
		return Server{}, err
	}
	return st.unseal(s.effective())
}

// view returns server id as the console shows it: its token stays sealed.
func (st *Store) view(ctx context.Context, id int64) (Server, error) {
	s, err := st.stored(ctx, id)
	if err != nil {
		return Server{}, err
	}
	return s.effective(), nil
}

func (st *Store) stored(ctx context.Context, id int64) (Server, error) {
	s, err := scan(st.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM git_servers g WHERE g.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

func (st *Store) unseal(s Server) (Server, error) {
	token, err := st.Box.Open(s.sealed)
	if err != nil {
		return Server{}, fmt.Errorf("git server %s: %w", s.Name, err)
	}
	s.token = token
	return s, nil
}

func (st *Store) Create(ctx context.Context, in Input) (Server, error) {
	var s Server
	token, err := in.apply(&s)
	if err != nil {
		return Server{}, err
	}
	var id int64
	if err := st.DB.QueryRowContext(ctx, `INSERT INTO git_servers(name, kind, base_url, api_url, token) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		s.Name, s.Kind, s.BaseURL, s.APIURL, st.Box.Seal(token)).Scan(&id); err != nil {
		return Server{}, duplicate(err)
	}
	return st.view(ctx, id)
}

// Update changes server id. The kind and base URL of a server that
// repositories use are fixed, since their URLs and links depend on them. A
// stored token only goes where it was entered for: changing the base or API
// URL needs the token entered again (or cleared).
func (st *Store) Update(ctx context.Context, id int64, in Input) (Server, error) {
	tx, err := st.DB.BeginTx(ctx, nil) // BEGIN IMMEDIATE: no other edit lands between the read and the write
	if err != nil {
		return Server{}, err
	}
	defer tx.Rollback()
	s, err := st.stored(ctx, id)
	if err != nil {
		return Server{}, err
	}
	was := s.effective()
	token, err := in.apply(&s)
	if err != nil {
		return Server{}, err
	}
	if s.RepositoryCount > 0 && (s.Kind != was.Kind || !strings.EqualFold(s.BaseURL, was.BaseURL)) {
		return Server{}, failure{ErrConflict, "repositories come from this server, so its kind and base URL cannot change"}
	}
	if now := s.effective(); s.sealed != "" && token == "" && !in.ClearToken && (now.BaseURL != was.BaseURL || now.APIURL != was.APIURL) {
		return Server{}, invalid("enter the access token again to change the server's address")
	}
	switch {
	case in.ClearToken:
		s.sealed = ""
	case token != "":
		s.sealed = st.Box.Seal(token)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE git_servers SET name = ?, kind = ?, base_url = ?, api_url = ?, token = ? WHERE id = ?`,
		s.Name, s.Kind, s.BaseURL, s.APIURL, s.sealed, id); err != nil {
		return Server{}, duplicate(err)
	}
	if err := tx.Commit(); err != nil {
		return Server{}, err
	}
	return st.view(ctx, id)
}

// Delete removes server id unless repositories come from it.
func (st *Store) Delete(ctx context.Context, id int64) error {
	res, err := st.DB.ExecContext(ctx, `DELETE FROM git_servers WHERE id = ?1 AND NOT EXISTS (SELECT 1 FROM repositories WHERE git_server_id = ?1)`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := st.stored(ctx, id); err != nil {
			return err
		}
		return failure{ErrConflict, "repositories come from this server, so it cannot be deleted"}
	}
	return nil
}

// Remote is how git fetches repository repoID from its server.
func (st *Store) Remote(ctx context.Context, repoID int64) (git.Remote, error) {
	var s Server
	var webURL string
	err := st.DB.QueryRowContext(ctx, `SELECT r.url, g.name, g.kind, g.token FROM repositories r JOIN git_servers g ON g.id = r.git_server_id WHERE r.id = ?`,
		repoID).Scan(&webURL, &s.Name, &s.Kind, &s.sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return git.Remote{}, fmt.Errorf("repository %d or its git server is gone", repoID)
	} else if err != nil {
		return git.Remote{}, err
	}
	if s, err = st.unseal(s); err != nil {
		return git.Remote{}, err
	}
	return s.Remote(webURL), nil
}

func duplicate(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return failure{ErrConflict, "a git server with this name or base URL already exists"}
	}
	return err
}
