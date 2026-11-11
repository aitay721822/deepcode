// Package store keeps the repository catalog in SQLite: the repositories,
// the snapshot each has published, and the snapshots' files and contents.
package store

import (
	"context"
	"database/sql"
	"errors"

	jobsstore "deepcode/internal/platform/jobs/store"
)

// Store runs the catalog's SQL.
type Store struct{ DB *sql.DB }

// ErrNotFound: no row matched.
var ErrNotFound = errors.New("找不到")

// The wiki statuses of a snapshot, as stored.
const (
	WikiNone       = "none"
	WikiGenerating = "generating"
	WikiReady      = "ready"
	WikiFailed     = "failed"
)

// Snapshot is the snapshot a repository has published.
type Snapshot struct {
	ID             int64  `json:"id"`
	CommitSHA      string `json:"commitSha"`
	IndexedAt      string `json:"indexedAt"`
	WikiStatus     string `json:"wikiStatus"` // one of the catalog's Wiki statuses
	EmbeddingModel string `json:"embeddingModel"`
	FileCount      int    `json:"fileCount"`
	ChunkCount     int    `json:"chunkCount"`
}

// Repo is an imported repository with its published snapshot (nil until the
// first index) and its latest job.
type Repo struct {
	ID            int64          `json:"-"`     // the row id, for joins and permissions
	PublicID      string         `json:"id"`    // a CUID2: the id in the API, URLs and the UI
	Owner         string         `json:"owner"` // GitLab: the group path, which may hold "/"
	Name          string         `json:"name"`
	URL           string         `json:"url"`      // the repository's web page
	Provider      string         `json:"provider"` // its git server's kind: github | gitlab
	ServerID      int64          `json:"serverId"`
	ServerName    string         `json:"serverName"`
	Description   string         `json:"description"`
	Stars         int            `json:"stars"`
	DefaultBranch string         `json:"defaultBranch"`
	TrackedBranch string         `json:"trackedBranch"` // "" follows DefaultBranch
	Snapshot      *Snapshot      `json:"snapshot"`
	Job           *jobsstore.Job `json:"job"` // left for the catalog to fill in
}

// NewRepo is a repository to add, from git server ServerID.
type NewRepo struct {
	URL, Owner, Name, Description, DefaultBranch string
	Stars                                        int
	ServerID                                     int64
}

// OnAdd writes in the transaction that adds repository id.
type OnAdd func(tx *sql.Tx, id int64) error

// RepoID returns the id of the repository at url, in any case, or ErrNotFound.
func (s *Store) RepoID(ctx context.Context, url string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM repositories WHERE lower(url) = lower(?)`, url).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// Add inserts r and runs onAdd, unless nil, in one transaction. A repository
// already at r.URL is returned with created false, and onAdd does not run for
// it; ErrNotFound means r's server is gone.
func (s *Store) Add(ctx context.Context, r NewRepo, onAdd OnAdd) (id int64, created bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil) // BEGIN IMMEDIATE: no other import lands between the check and the insert
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE lower(url) = lower(?)`, r.URL).Scan(&id)
	if err == nil {
		return id, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	// Selecting the server keeps a server deleted since it was resolved from
	// getting a repository.
	err = tx.QueryRowContext(ctx, `
		INSERT INTO repositories(url, owner, name, description, stars, default_branch, git_server_id)
		SELECT ?, ?, ?, ?, ?, ?, id FROM git_servers WHERE id = ?
		RETURNING id`, r.URL, r.Owner, r.Name, r.Description, r.Stars, r.DefaultBranch, r.ServerID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, ErrNotFound
	}
	if err == nil && onAdd != nil {
		err = onAdd(tx, id)
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, tx.Commit()
}

// Published is what a sync weighs of the snapshot a repository has
// published: all zero before its first.
type Published struct {
	CommitSHA, EmbeddingModel, WikiStatus string
	Analyzer                              int // the analyzer version that read it
}

// Published returns what repository id has published, or ErrNotFound.
func (s *Store) Published(ctx context.Context, id int64) (Published, error) {
	var sha, model, wikiStatus sql.NullString
	var read sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `
		SELECT s.commit_sha, s.embedding_model, s.wiki_status, s.analyzer
		FROM repositories r LEFT JOIN snapshots s ON s.id = r.ready_snapshot_id WHERE r.id = ?`, id).Scan(&sha, &model, &wikiStatus, &read)
	if errors.Is(err, sql.ErrNoRows) {
		return Published{}, ErrNotFound
	}
	return Published{sha.String, model.String, wikiStatus.String, int(read.Int64)}, err
}

// Analyzer returns the analyzer version that read snapshot snap.
func (s *Store) Analyzer(ctx context.Context, snap int64) (int, error) {
	var read int
	err := s.DB.QueryRowContext(ctx, `SELECT analyzer FROM snapshots WHERE id = ?`, snap).Scan(&read)
	return read, err
}

// ID returns the row id of the repository whose public id is publicID, or
// ErrNotFound.
func (s *Store) ID(ctx context.Context, publicID string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM repositories WHERE public_id = ?`, publicID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// Repos returns the repositories visible accepts (nil: all), newest first,
// without their jobs.
func (s *Store) Repos(ctx context.Context, visible func(id int64) bool) ([]Repo, error) {
	return s.repos(ctx, visible, "")
}

// Repo returns repository id without its job, or ErrNotFound.
func (s *Store) Repo(ctx context.Context, id int64) (Repo, error) {
	list, err := s.repos(ctx, nil, "WHERE r.id = ?", id)
	if err != nil {
		return Repo{}, err
	}
	if len(list) == 0 {
		return Repo{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) repos(ctx context.Context, visible func(int64) bool, where string, args ...any) ([]Repo, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.id, r.public_id, r.owner, r.name, r.url, g.kind, g.id, g.name, r.description, r.stars, r.default_branch, r.tracked_branch,
			s.id, s.commit_sha, coalesce(s.indexed_at, ''), s.wiki_status, s.embedding_model, s.file_count, s.chunk_count
		FROM repositories r JOIN git_servers g ON g.id = r.git_server_id LEFT JOIN snapshots s ON s.id = r.ready_snapshot_id
		`+where+` ORDER BY r.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // before the caller's job query, which needs a connection of its own
	out := []Repo{}
	for rows.Next() {
		var v Repo
		var sid sql.NullInt64
		var sha, indexed, wikiStatus, model sql.NullString
		var files, chunks sql.NullInt64
		if err := rows.Scan(&v.ID, &v.PublicID, &v.Owner, &v.Name, &v.URL, &v.Provider, &v.ServerID, &v.ServerName, &v.Description, &v.Stars, &v.DefaultBranch, &v.TrackedBranch,
			&sid, &sha, &indexed, &wikiStatus, &model, &files, &chunks); err != nil {
			return nil, err
		}
		if visible != nil && !visible(v.ID) {
			continue
		}
		if sid.Valid {
			v.Snapshot = &Snapshot{sid.Int64, sha.String, indexed.String, wikiStatus.String, model.String, int(files.Int64), int(chunks.Int64)}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
