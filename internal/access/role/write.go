package role

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"deepcode/internal/access/role/store"
)

// Input creates or changes a role; nil fields keep their value.
type Input struct {
	Name            *string       `json:"name"`
	Description     *string       `json:"description"`
	Permissions     *[]Permission `json:"permissions"`
	AllRepositories *bool         `json:"allRepositories"`
	RepositoryIDs   *[]string     `json:"repositoryIds"` // public ids
}

type failure struct {
	kind error
	msg  string
}

func (f failure) Error() string        { return f.msg }
func (f failure) Is(target error) bool { return target == f.kind }

func (s *Service) Create(ctx context.Context, in Input) (Role, error) {
	var r Role
	if err := fill(&r, in); err != nil {
		return Role{}, err
	}
	id, err := s.Store.Create(ctx, r, in.Permissions, in.RepositoryIDs)
	if err != nil {
		return Role{}, explain(err)
	}
	return s.Get(ctx, id)
}

// Update changes role id. Built-in roles keep their names, and the admin role
// keeps every permission on every repository.
func (s *Service) Update(ctx context.Context, id int64, in Input) (Role, error) {
	// Checked within the store's transaction, against the role as it is then.
	err := s.Store.Update(ctx, id, func(r *Role) error {
		if r.Builtin && in.Name != nil && strings.TrimSpace(*in.Name) != r.Name {
			return failure{ErrConflict, "內建角色不能重新命名"}
		}
		if id == Admin && !unchangedAdmin(in) {
			return failure{ErrConflict, "admin 角色永遠擁有所有 Repository 的所有權限"}
		}
		return fill(r, in)
	}, in.Permissions, in.RepositoryIDs)
	if err != nil {
		return Role{}, explain(err)
	}
	return s.Get(ctx, id)
}

// unchangedAdmin reports whether in keeps the admin role's grants as they
// are, as the admin console does when it sends the whole role back.
func unchangedAdmin(in Input) bool {
	if in.AllRepositories != nil && !*in.AllRepositories || in.RepositoryIDs != nil && len(*in.RepositoryIDs) > 0 {
		return false
	}
	if in.Permissions == nil {
		return true
	}
	have := map[Permission]bool{}
	for _, p := range *in.Permissions {
		have[p] = true
	}
	for _, i := range Permissions {
		if !have[i.Name] {
			return false
		}
	}
	return true
}

// Delete removes role id; built-in roles stay.
func (s *Service) Delete(ctx context.Context, id int64) error {
	r, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if r.Builtin {
		return failure{ErrConflict, "內建角色不能刪除"}
	}
	return s.Store.Delete(ctx, id)
}

// fill validates in and applies its fields to r.
func fill(r *Role, in Input) error {
	if in.Name != nil {
		r.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		r.Description = strings.TrimSpace(*in.Description)
	}
	if in.AllRepositories != nil {
		r.AllRepositories = *in.AllRepositories
	}
	switch {
	case r.Name == "" || utf8.RuneCountInString(r.Name) > 64: // characters, not bytes
		return failure{ErrInvalid, "名稱必須為 1 到 64 個字元"}
	case utf8.RuneCountInString(r.Description) > 500:
		return failure{ErrInvalid, "描述超過 500 個字元"}
	}
	if in.Permissions != nil {
		for _, p := range *in.Permissions {
			if !known(p) {
				return failure{ErrInvalid, fmt.Sprintf("未知的權限 %q", p)}
			}
		}
	}
	return nil
}

// explain words the store's refusals as errors a caller can act on.
func explain(err error) error {
	switch {
	case errors.Is(err, store.ErrDuplicate):
		return failure{ErrConflict, "已有同名的角色"}
	case errors.Is(err, store.ErrNoRepository):
		return failure{ErrInvalid, "Repository 不存在"}
	}
	return err
}
