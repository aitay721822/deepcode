package store

import (
	"context"
	"database/sql"
	"errors"
)

// Ref names one snapshot of a repository.
type Ref struct {
	ID        int64
	CommitSHA string
}

// Resolve finds snapshot id, which must belong to repository repoID and be
// ready (indexed and not pruned), or the published snapshot when id is 0; or
// returns ErrNotFound.
func (s *Store) Resolve(ctx context.Context, repoID, id int64) (Ref, error) {
	q := `SELECT s.id, s.commit_sha FROM repositories r JOIN snapshots s ON s.id = r.ready_snapshot_id WHERE r.id = ?`
	args := []any{repoID}
	if id != 0 {
		q = `SELECT id, commit_sha FROM snapshots WHERE repository_id = ? AND id = ? AND status = 'ready'`
		args = append(args, id)
	}
	var ref Ref
	err := s.DB.QueryRowContext(ctx, q, args...).Scan(&ref.ID, &ref.CommitSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return Ref{}, ErrNotFound
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

// Files lists the files of snapshot by path.
func (s *Store) Files(ctx context.Context, snapshot int64) ([]File, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path, language, size, line_count FROM source_files WHERE snapshot_id = ? ORDER BY path`, snapshot)
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
func (s *Store) Changed(ctx context.Context, from, to int64) (map[string]bool, int, error) {
	var files int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM source_files WHERE snapshot_id = ?`, to).Scan(&files); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `
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

// Content returns the language and content of path in snapshot snap, or
// ErrNotFound.
func (s *Store) Content(ctx context.Context, snap int64, path string) (language, content string, err error) {
	err = s.DB.QueryRowContext(ctx, `
		SELECT f.language, c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND f.path = ?`, snap, path).Scan(&language, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return language, content, err
}
