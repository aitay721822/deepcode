package catalog

import (
	"context"
	"database/sql"
	"errors"

	"deepcode/internal/platform/jobs"
)

// Snapshot is the snapshot a repository has published.
type Snapshot struct {
	ID             int64  `json:"id"`
	CommitSHA      string `json:"commitSha"`
	IndexedAt      string `json:"indexedAt"`
	WikiStatus     string `json:"wikiStatus"` // one of the Wiki statuses below
	EmbeddingModel string `json:"embeddingModel"`
	FileCount      int    `json:"fileCount"`
	ChunkCount     int    `json:"chunkCount"`
}

// The wiki statuses of a snapshot: wiki writes them, Sync and the pages read them.
const (
	WikiNone       = "none"
	WikiGenerating = "generating"
	WikiReady      = "ready"
	WikiFailed     = "failed"
)

// Repo is an imported repository with its published snapshot (nil until the
// first index) and its latest job.
type Repo struct {
	ID            int64     `json:"-"`     // the row id, for joins and permissions
	PublicID      string    `json:"id"`    // a CUID2: the id in the API, URLs and the UI
	Owner         string    `json:"owner"` // GitLab: the group path, which may hold "/"
	Name          string    `json:"name"`
	URL           string    `json:"url"`      // the repository's web page
	Provider      string    `json:"provider"` // its git server's kind: github | gitlab
	ServerID      int64     `json:"serverId"`
	ServerName    string    `json:"serverName"`
	Description   string    `json:"description"`
	Stars         int       `json:"stars"`
	DefaultBranch string    `json:"defaultBranch"`
	Snapshot      *Snapshot `json:"snapshot"`
	Job           *jobs.Job `json:"job"`
}

// List returns the repositories visible accepts (nil: all), newest first.
func (c *Catalog) List(ctx context.Context, visible func(id int64) bool) ([]Repo, error) {
	return c.repos(ctx, visible, "")
}

// ID returns the row id of the repository whose public id is publicID, or an
// ErrNotFound error: a row id ("1") finds none.
func (c *Catalog) ID(ctx context.Context, publicID string) (int64, error) {
	var id int64
	err := c.DB.QueryRowContext(ctx, `SELECT id FROM repositories WHERE public_id = ?`, publicID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, failf(ErrNotFound, "repository not found")
	}
	return id, err
}

// Get returns repository id, or an ErrNotFound error.
func (c *Catalog) Get(ctx context.Context, id int64) (Repo, error) {
	list, err := c.repos(ctx, nil, "WHERE r.id = ?", id)
	if err != nil {
		return Repo{}, err
	}
	if len(list) == 0 {
		return Repo{}, failf(ErrNotFound, "repository not found")
	}
	return list[0], nil
}

func (c *Catalog) repos(ctx context.Context, visible func(int64) bool, where string, args ...any) ([]Repo, error) {
	rows, err := c.DB.QueryContext(ctx, `
		SELECT r.id, r.public_id, r.owner, r.name, r.url, g.kind, g.id, g.name, r.description, r.stars, r.default_branch,
			s.id, s.commit_sha, coalesce(s.indexed_at, ''), s.wiki_status, s.embedding_model, s.file_count, s.chunk_count
		FROM repositories r JOIN git_servers g ON g.id = r.git_server_id LEFT JOIN snapshots s ON s.id = r.ready_snapshot_id
		`+where+` ORDER BY r.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	out := []Repo{}
	for rows.Next() {
		var v Repo
		var sid sql.NullInt64
		var sha, indexed, wikiStatus, model sql.NullString
		var files, chunks sql.NullInt64
		if err := rows.Scan(&v.ID, &v.PublicID, &v.Owner, &v.Name, &v.URL, &v.Provider, &v.ServerID, &v.ServerName, &v.Description, &v.Stars, &v.DefaultBranch,
			&sid, &sha, &indexed, &wikiStatus, &model, &files, &chunks); err != nil {
			rows.Close()
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
	rows.Close() // before the job query below, which needs a connection of its own
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]int64, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	latest, err := c.Queue.LatestOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Job = latest[out[i].ID]
	}
	return out, nil
}
