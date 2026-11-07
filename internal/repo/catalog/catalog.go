// Package catalog is the repository catalog: it imports and syncs
// repositories from the configured git servers, queueing the jobs that index
// them, and reads what each repository has published: its snapshot, file
// list and file contents.
package catalog

import (
	"errors"
	"fmt"

	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
)

type Catalog struct {
	Store      *store.Store
	Queue      *jobs.Queue
	Git        *git.Client
	Servers    *gitserver.Service
	Cache      *cache.Cache // file lists
	MaxRepoKB  int64
	ChatModel  string // "" when no chat model is configured: no wiki
	EmbedModel string // "" when embeddings are off
}

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = store.ErrNotFound
	ErrInvalid  = errors.New("無效的請求")
	ErrTooLarge = errors.New("太大")
	ErrUpstream = errors.New("上游伺服器錯誤") // the git server or its API
)

// kinded classifies err as kind: errors.Is(e, kind) holds and the message
// stays err's own.
type kinded struct{ kind, err error }

func (e kinded) Error() string   { return e.err.Error() }
func (e kinded) Unwrap() []error { return []error{e.kind, e.err} }

func failf(kind error, format string, args ...any) error {
	return kinded{kind, fmt.Errorf(format, args...)}
}
