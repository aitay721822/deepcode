package role

import (
	"context"

	"deepcode/internal/access/role/store"
)

// Grants is what a user's roles allow. Each role grants its permissions on
// every repository or on its own list, and roles are never merged: a
// permission on a repository needs one role that grants both.
type Grants []store.RoleGrant

// GrantsOf reads what user id's roles allow.
func (s *Service) GrantsOf(ctx context.Context, id int64) (Grants, error) {
	g, err := s.Store.GrantsOf(ctx, id)
	return Grants(g), err
}

// Can reports whether a role grants perm (on some repository, for a scoped one).
func (g Grants) Can(perm Permission) bool {
	for _, r := range g {
		if r.Perms[perm] {
			return true
		}
	}
	return false
}

// CanOn reports whether a role grants perm on repository repoID.
func (g Grants) CanOn(perm Permission, repoID int64) bool {
	for _, r := range g {
		if r.Perms[perm] && (r.All || r.Repos[repoID]) {
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
		if r.All && r.Perms[RepoRead] {
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
		if r.Perms[RepoImport] && !r.All {
			out = append(out, r.Role)
		}
	}
	return out
}
