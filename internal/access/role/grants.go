package role

import (
	"context"
	"database/sql"
)

// Grants is what a user's roles allow. Each role grants its permissions on
// every repository or on its own list, and roles are never merged: a
// permission on a repository needs one role that grants both.
type Grants []grant

type grant struct {
	role  int64
	all   bool
	perms map[Permission]bool
	repos map[int64]bool
}

// GrantsOf reads what user id's roles allow.
func (s *Store) GrantsOf(ctx context.Context, id int64) (Grants, error) {
	var out Grants
	err := scanPairs(ctx, s.DB, `SELECT r.id, r.all_repositories FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ? ORDER BY r.id`, func(role int64, all bool) {
		out = append(out, grant{role: role, all: all, perms: map[Permission]bool{}, repos: map[int64]bool{}})
	}, id)
	if err != nil {
		return nil, err
	}
	byRole := map[int64]*grant{}
	for i := range out {
		byRole[out[i].role] = &out[i]
	}
	const mine = `role_id IN (SELECT role_id FROM user_roles WHERE user_id = ?)`
	err = scanPairs(ctx, s.DB, `SELECT role_id, permission FROM role_permissions WHERE `+mine, func(role int64, p Permission) {
		if g := byRole[role]; g != nil {
			g.perms[p] = true
		}
	}, id)
	if err != nil {
		return nil, err
	}
	err = scanPairs(ctx, s.DB, `SELECT role_id, repository_id FROM role_repositories WHERE `+mine, func(role, repo int64) {
		if g := byRole[role]; g != nil {
			g.repos[repo] = true
		}
	}, id)
	return out, err
}

// Can reports whether a role grants perm (on some repository, for a scoped one).
func (g Grants) Can(perm Permission) bool {
	for _, r := range g {
		if r.perms[perm] {
			return true
		}
	}
	return false
}

// CanOn reports whether a role grants perm on repository repoID.
func (g Grants) CanOn(perm Permission, repoID int64) bool {
	for _, r := range g {
		if r.perms[perm] && (r.all || r.repos[repoID]) {
			return true
		}
	}
	return false
}

// Permissions lists what any role grants, in Permissions order.
func (g Grants) Permissions() []Permission {
	out := []Permission{}
	for _, info := range Permissions {
		if g.Can(info.Name) {
			out = append(out, info.Name)
		}
	}
	return out
}

// On lists the repository permissions (repoScoped) a role grants on
// repository repoID.
func (g Grants) On(repoID int64) []Permission {
	out := []Permission{}
	for _, p := range repoScoped {
		if g.CanOn(p, repoID) {
			out = append(out, p)
		}
	}
	return out
}

// AllRepositories reports whether a role lets the user read every repository.
func (g Grants) AllRepositories() bool {
	for _, r := range g {
		if r.all && r.perms[RepoRead] {
			return true
		}
	}
	return false
}

// ImportRoles are the roles a repository the user imports joins: those that
// grant repo.import on listed repositories only.
func (g Grants) ImportRoles() []int64 {
	var out []int64
	for _, r := range g {
		if r.perms[RepoImport] && !r.all {
			out = append(out, r.role)
		}
	}
	return out
}

// Grant adds repository repoID to roles within tx, so that a new repository
// joins its importer's roles in the transaction that adds it.
func Grant(ctx context.Context, tx *sql.Tx, roles []int64, repoID int64) error {
	for _, id := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO role_repositories(role_id, repository_id) VALUES (?, ?)`, id, repoID); err != nil {
			return err
		}
	}
	return nil
}
