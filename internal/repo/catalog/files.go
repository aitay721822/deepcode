package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"deepcode/internal/repo/catalog/store"
)

type (
	Ref  = store.Ref  // one snapshot of a repository
	File = store.File // one file of a snapshot, as the file list shows it
)

// Resolve finds snapshot id, which must belong to repository repoID and be
// ready (indexed and not pruned), or the published snapshot when id is 0.
func (c *Catalog) Resolve(ctx context.Context, repoID, id int64) (Ref, error) {
	ref, err := c.Store.Resolve(ctx, repoID, id)
	if errors.Is(err, ErrNotFound) {
		return Ref{}, failf(ErrNotFound, "沒有已建立索引的版本")
	}
	return ref, err
}

// FilesJSON lists the files of snapshot by path, as a JSON array of File.
// A snapshot never changes, so the list is cached for an hour.
func (c *Catalog) FilesJSON(ctx context.Context, snapshot int64) ([]byte, error) {
	return c.Cache.GetOrLoad(ctx, fmt.Sprintf("files:%d", snapshot), time.Hour, func() ([]byte, error) {
		// Every waiter shares this load: the first caller leaving must not fail the rest.
		files, err := c.Store.Files(context.WithoutCancel(ctx), snapshot)
		if err != nil {
			return nil, err
		}
		return json.Marshal(files)
	})
}

// Changed lists the files added, edited or removed between snapshots from and
// to, and counts the files of to.
func (c *Catalog) Changed(ctx context.Context, from, to int64) (map[string]bool, int, error) {
	return c.Store.Changed(ctx, from, to)
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
	var err error
	f.Language, f.Content, err = c.Store.Content(ctx, snap.ID, path)
	if errors.Is(err, ErrNotFound) {
		return Content{}, failf(ErrNotFound, "在這個版本中找不到此檔案")
	}
	return f, err
}
