package role

import (
	"context"
	"errors"
	"sort"

	"deepcode/internal/access/role/store"
)

// Admin is the built-in role holding every permission on every repository.
const Admin = store.Admin

// Member is the built-in role for reading and asking about the repositories
// granted to it; visitors who sign themselves up get it.
const Member = store.Member

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = store.ErrNotFound
	ErrInvalid  = errors.New("無效的角色")
	ErrConflict = errors.New("角色衝突") // a duplicate name, or a built-in role
)

type Role = store.Role

// Service applies the role rules over the roles in Store.
type Service struct{ Store *store.Store }

// List returns every role by name, built-in ones first.
func (s *Service) List(ctx context.Context) ([]Role, error) {
	list, err := s.Store.List(ctx)
	for i := range list {
		inOrder(list[i].Permissions)
	}
	return list, err
}

func (s *Service) Get(ctx context.Context, id int64) (Role, error) {
	r, err := s.Store.Get(ctx, id)
	inOrder(r.Permissions)
	return r, err
}

// inOrder sorts perms into Permissions order.
func inOrder(perms []Permission) {
	sort.Slice(perms, func(a, b int) bool { return order(perms[a]) < order(perms[b]) })
}

// order is a permission's place in Permissions.
func order(p Permission) int {
	for i, info := range Permissions {
		if info.Name == p {
			return i
		}
	}
	return len(Permissions)
}
