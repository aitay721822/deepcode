// Package importer imports repositories on a user's behalf. It joins two
// areas that do not know each other: the catalog adds the repository, and in
// the same transaction the repository joins the user's import roles, so the
// user can read what they imported.
package importer

import (
	"context"
	"database/sql"
	"errors"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/repo/catalog"
)

// ErrForbidden: the repository exists, but the user may not read it.
var ErrForbidden = errors.New("this repository was imported before; ask an administrator for access")

type Service struct{ Catalog *catalog.Catalog }

// Import imports raw (a repository URL, or a path on git server serverID)
// for p. A new repository joins p's import roles and created is true; one
// imported before is returned when p may read it.
func (s *Service) Import(ctx context.Context, p *session.Principal, raw string, serverID int64) (repo catalog.Repo, created bool, err error) {
	repo, created, err = s.Catalog.Import(ctx, raw, serverID, func(tx *sql.Tx, id int64) error {
		return role.Grant(ctx, tx, p.ImportRoles(), id)
	})
	if err == nil && !created && !p.CanOn(role.RepoRead, repo.ID) {
		return catalog.Repo{}, false, ErrForbidden
	}
	return repo, created, err
}
