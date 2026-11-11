package catalog

import (
	"context"
	"errors"

	"deepcode/internal/repo/catalog/store"
)

type (
	Snapshot = store.Snapshot // the snapshot a repository has published
	Repo     = store.Repo     // an imported repository with its published snapshot and latest job
)

// The wiki statuses of a snapshot: wiki writes them, Sync and the pages read them.
const (
	WikiNone       = store.WikiNone
	WikiGenerating = store.WikiGenerating
	WikiReady      = store.WikiReady
	WikiFailed     = store.WikiFailed
)

// List returns the repositories visible accepts (nil: all), newest first.
func (c *Catalog) List(ctx context.Context, visible func(id int64) bool) ([]Repo, error) {
	list, err := c.Store.Repos(ctx, visible)
	if err != nil {
		return nil, err
	}
	return c.withJobs(ctx, list)
}

// ID returns the row id of the repository whose public id is publicID, or an
// ErrNotFound error: a row id ("1") finds none.
func (c *Catalog) ID(ctx context.Context, publicID string) (int64, error) {
	id, err := c.Store.ID(ctx, publicID)
	if errors.Is(err, ErrNotFound) {
		return 0, failf(ErrNotFound, "找不到 Repository")
	}
	return id, err
}

// Get returns repository id, or an ErrNotFound error.
func (c *Catalog) Get(ctx context.Context, id int64) (Repo, error) {
	r, err := c.Store.Repo(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Repo{}, failf(ErrNotFound, "找不到 Repository")
	} else if err != nil {
		return Repo{}, err
	}
	list, err := c.withJobs(ctx, []Repo{r})
	if err != nil {
		return Repo{}, err
	}
	return list[0], nil
}

// withJobs gives each repository of list its latest job, in one query.
func (c *Catalog) withJobs(ctx context.Context, list []Repo) ([]Repo, error) {
	ids := make([]int64, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	latest, err := c.Queue.LatestOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Job = latest[list[i].ID]
	}
	return list, nil
}

// Tracked returns the branch a user set, "" when the repository follows its
// remote's HEAD.
func (c *Catalog) Tracked(ctx context.Context, id int64) (string, error) {
	tracked, _, err := c.Store.Branch(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return "", failf(ErrNotFound, "找不到 Repository")
	}
	return tracked, err
}

// Branch returns the branch repository id follows: the one a user set, else
// its default, "" when neither is known.
func (c *Catalog) Branch(ctx context.Context, id int64) (string, error) {
	tracked, def, err := c.Store.Branch(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return "", failf(ErrNotFound, "找不到 Repository")
	} else if err != nil {
		return "", err
	}
	if tracked != "" {
		return tracked, nil
	}
	return def, nil
}

// Branches returns the remote's branch names, sorted.
func (c *Catalog) Branches(ctx context.Context, id int64) ([]string, error) {
	remote, err := c.Servers.Remote(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.Git.Branches(ctx, remote)
}

// SetBranch sets the branch repository id follows; "" follows its default.
// It does not sync: the caller queues that once the name is stored.
func (c *Catalog) SetBranch(ctx context.Context, id int64, branch string) error {
	if branch != "" && !c.Git.ValidBranch(ctx, branch) {
		return failf(ErrInvalid, "Branch 名稱無效：%q", branch)
	}
	err := c.Store.SetBranch(ctx, id, branch)
	if errors.Is(err, ErrNotFound) {
		return failf(ErrNotFound, "找不到 Repository")
	}
	return err
}
