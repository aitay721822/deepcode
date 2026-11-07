package account

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"deepcode/internal/access/account/store"
)

// List returns every user by username.
func (s *Service) List(ctx context.Context) ([]User, error) {
	return s.Store.List(ctx)
}

func (s *Service) Get(ctx context.Context, id int64) (User, error) {
	return s.Store.Get(ctx, id)
}

// FirstAdmin returns the id of the oldest user holding the admin role.
func (s *Service) FirstAdmin(ctx context.Context) (int64, error) {
	return s.Store.FirstAdmin(ctx)
}

// ByUsername returns the user called username, in any letter case.
func (s *Service) ByUsername(ctx context.Context, username string) (User, error) {
	return s.Store.ByUsername(ctx, strings.TrimSpace(username))
}

func (s *Service) Create(ctx context.Context, in Input) (User, error) {
	if in.Username == nil || !usernameRe.MatchString(strings.TrimSpace(*in.Username)) {
		return User{}, failure{ErrInvalid, "使用者名稱必須為 2 到 32 個英文字母、數字、「.」、「_」或「-」，並以英文字母或數字開頭"}
	}
	name := strings.TrimSpace(*in.Username)
	display := name
	if in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) != "" {
		display = strings.TrimSpace(*in.DisplayName)
	}
	if in.Password == nil {
		return User{}, failure{ErrInvalid, "密碼為必填"}
	}
	if err := errors.Join(checkDisplay(display), checkPassword(*in.Password)); err != nil {
		return User{}, err
	}
	encoded, err := hash(*in.Password)
	if err != nil {
		return User{}, err
	}
	id, err := s.Store.Create(ctx, name, display, encoded, in.RoleIDs)
	if err != nil {
		return User{}, explain(err)
	}
	return s.Get(ctx, id)
}

// Update changes user id on behalf of actor, the signed-in user. A new
// password or disabling the user signs them out everywhere.
func (s *Service) Update(ctx context.Context, id, actor int64, in Input) (User, error) {
	if in.Disabled != nil && *in.Disabled && id == actor {
		return User{}, failure{ErrConflict, "你不能停用自己的帳號"}
	}
	c := store.Change{Disabled: in.Disabled, RoleIDs: in.RoleIDs}
	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if err := checkDisplay(name); err != nil {
			return User{}, err
		}
		c.DisplayName = &name
	}
	if in.Password != nil {
		if err := checkPassword(*in.Password); err != nil {
			return User{}, err
		}
		h, err := hash(*in.Password)
		if err != nil {
			return User{}, err
		}
		c.PasswordHash = &h
	}
	c.SignOut = c.PasswordHash != nil || in.Disabled != nil && *in.Disabled
	if err := s.Store.Update(ctx, id, c); err != nil {
		return User{}, explain(err)
	}
	return s.Get(ctx, id)
}

// Delete removes user id on behalf of actor, with the user's sessions and conversations.
func (s *Service) Delete(ctx context.Context, id, actor int64) error {
	if id == actor {
		return failure{ErrConflict, "你不能刪除自己的帳號"}
	}
	return explain(s.Store.Delete(ctx, id))
}

// checkDisplay wants 1 to 64 characters (not bytes), none of them a control
// character or a line break, which would break the line a name is shown on,
// or a bidi control, which would reorder the text around it. Other invisible
// characters stay: Persian and Indic names and emoji sequences need joiners.
func checkDisplay(name string) error {
	disruptive := strings.ContainsFunc(name, func(r rune) bool {
		return unicode.In(r, unicode.Cc, unicode.Zl, unicode.Zp, unicode.Bidi_Control)
	})
	if n := utf8.RuneCountInString(name); n == 0 || n > 64 || disruptive {
		return failure{ErrInvalid, "顯示名稱必須為 1 到 64 個字元，且不能包含控制字元、換行或文字方向標記"}
	}
	return nil
}

// explain words the store's refusals as errors a caller can act on.
func explain(err error) error {
	switch {
	case errors.Is(err, store.ErrTaken):
		return failure{ErrConflict, "這個使用者名稱已被使用"}
	case errors.Is(err, store.ErrNoRole):
		return failure{ErrInvalid, "角色不存在"}
	case errors.Is(err, store.ErrLastAdmin):
		return failure{ErrConflict, "至少要有一位啟用中的使用者保有 admin 角色"}
	}
	return err
}
