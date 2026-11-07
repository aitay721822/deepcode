package store

import "context"

// List returns every role by name, built-in ones first.
func (s *Store) List(ctx context.Context) ([]Role, error) {
	return s.roles(ctx, "ORDER BY builtin DESC, name")
}

func (s *Store) Get(ctx context.Context, id int64) (Role, error) {
	list, err := s.roles(ctx, "WHERE id = ?", id)
	if err != nil {
		return Role{}, err
	}
	if len(list) == 0 {
		return Role{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) roles(ctx context.Context, tail string, args ...any) ([]Role, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, description, builtin, all_repositories,
		(SELECT count(*) FROM user_roles u WHERE u.role_id = roles.id) FROM roles `+tail, args...)
	if err != nil {
		return nil, err
	}
	out := []Role{}
	index := map[int64]int{}
	for rows.Next() {
		r := Role{Permissions: []Permission{}, RepositoryIDs: []string{}}
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Builtin, &r.AllRepositories, &r.UserCount); err != nil {
			rows.Close()
			return nil, err
		}
		index[r.ID] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// ponytail: both link tables are read whole; filter by role once there are thousands of roles.
	err = scanPairs(ctx, s.DB, `SELECT role_id, permission FROM role_permissions ORDER BY permission`, func(id int64, p Permission) {
		if i, ok := index[id]; ok {
			out[i].Permissions = append(out[i].Permissions, p)
		}
	})
	if err != nil {
		return nil, err
	}
	err = scanPairs(ctx, s.DB, `SELECT rr.role_id, r.public_id FROM role_repositories rr JOIN repositories r ON r.id = rr.repository_id
		ORDER BY rr.repository_id`, func(id int64, repo string) {
		if i, ok := index[id]; ok {
			out[i].RepositoryIDs = append(out[i].RepositoryIDs, repo)
		}
	})
	return out, err
}

// RoleGrant is what one of a user's roles allows: its permissions, on every
// repository (All) or on Repos.
type RoleGrant struct {
	Role  int64
	All   bool
	Perms map[Permission]bool
	Repos map[int64]bool
}

// GrantsOf reads what each of user id's roles allows, by role id.
func (s *Store) GrantsOf(ctx context.Context, id int64) ([]RoleGrant, error) {
	var out []RoleGrant
	err := scanPairs(ctx, s.DB, `SELECT r.id, r.all_repositories FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ? ORDER BY r.id`, func(role int64, all bool) {
		out = append(out, RoleGrant{Role: role, All: all, Perms: map[Permission]bool{}, Repos: map[int64]bool{}})
	}, id)
	if err != nil {
		return nil, err
	}
	byRole := map[int64]*RoleGrant{}
	for i := range out {
		byRole[out[i].Role] = &out[i]
	}
	const mine = `role_id IN (SELECT role_id FROM user_roles WHERE user_id = ?)`
	err = scanPairs(ctx, s.DB, `SELECT role_id, permission FROM role_permissions WHERE `+mine, func(role int64, p Permission) {
		if g := byRole[role]; g != nil {
			g.Perms[p] = true
		}
	}, id)
	if err != nil {
		return nil, err
	}
	err = scanPairs(ctx, s.DB, `SELECT role_id, repository_id FROM role_repositories WHERE `+mine, func(role, repo int64) {
		if g := byRole[role]; g != nil {
			g.Repos[repo] = true
		}
	}, id)
	return out, err
}
