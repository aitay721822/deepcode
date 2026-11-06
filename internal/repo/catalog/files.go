package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Ref names one snapshot of a repository.
type Ref struct {
	ID        int64
	CommitSHA string
}

// Resolve finds snapshot id, which must belong to repository repoID and be
// ready (indexed and not pruned), or the published snapshot when id is 0.
func (c *Catalog) Resolve(ctx context.Context, repoID, id int64) (Ref, error) {
	q := `SELECT s.id, s.commit_sha FROM repositories r JOIN snapshots s ON s.id = r.ready_snapshot_id WHERE r.id = ?`
	args := []any{repoID}
	if id != 0 {
		q = `SELECT id, commit_sha FROM snapshots WHERE repository_id = ? AND id = ? AND status = 'ready'`
		args = append(args, id)
	}
	var ref Ref
	err := c.DB.QueryRowContext(ctx, q, args...).Scan(&ref.ID, &ref.CommitSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return Ref{}, failf(ErrNotFound, "no indexed snapshot")
	}
	return ref, err
}

// File is one file of a snapshot, as the file list shows it.
type File struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
	Lines    int    `json:"lines"`
}

// FilesJSON lists the files of snapshot by path, as a JSON array of File.
// A snapshot never changes, so the list is cached for an hour.
func (c *Catalog) FilesJSON(ctx context.Context, snapshot int64) ([]byte, error) {
	return c.Cache.GetOrLoad(ctx, fmt.Sprintf("files:%d", snapshot), time.Hour, func() ([]byte, error) {
		// Every waiter shares this load: the first caller leaving must not fail the rest.
		files, err := c.files(context.WithoutCancel(ctx), snapshot)
		if err != nil {
			return nil, err
		}
		return json.Marshal(files)
	})
}

func (c *Catalog) files(ctx context.Context, snapshot int64) ([]File, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT path, language, size, line_count FROM source_files WHERE snapshot_id = ? ORDER BY path`, snapshot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []File{}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.Language, &f.Size, &f.Lines); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// Changed lists the files added, edited or removed between snapshots from and
// to, and counts the files of to.
func (c *Catalog) Changed(ctx context.Context, from, to int64) (map[string]bool, int, error) {
	var files int
	if err := c.DB.QueryRowContext(ctx, `SELECT count(*) FROM source_files WHERE snapshot_id = ?`, to).Scan(&files); err != nil {
		return nil, 0, err
	}
	rows, err := c.DB.QueryContext(ctx, `
		SELECT path FROM source_files n WHERE snapshot_id = ?2 AND NOT EXISTS (
			SELECT 1 FROM source_files o WHERE o.snapshot_id = ?1 AND o.path = n.path AND o.content_hash = n.content_hash)
		UNION
		SELECT path FROM source_files o WHERE snapshot_id = ?1 AND NOT EXISTS (
			SELECT 1 FROM source_files n WHERE n.snapshot_id = ?2 AND n.path = o.path)`, from, to)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	changed := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, 0, err
		}
		changed[p] = true
	}
	return changed, files, rows.Err()
}

// Content is one file as of a snapshot.
type Content struct {
	Path       string `json:"path"`
	Language   string `json:"language"`
	Content    string `json:"content"`
	CommitSHA  string `json:"commitSha"`
	SnapshotID int64  `json:"snapshotId"`
}

// File returns path as of snap, or an ErrNotFound error.
func (c *Catalog) File(ctx context.Context, snap Ref, path string) (Content, error) {
	f := Content{Path: path, CommitSHA: snap.CommitSHA, SnapshotID: snap.ID}
	err := c.DB.QueryRowContext(ctx, `
		SELECT f.language, c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND f.path = ?`, snap.ID, path).Scan(&f.Language, &f.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return Content{}, failf(ErrNotFound, "file not found in this snapshot")
	}
	return f, err
}
