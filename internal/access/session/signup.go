package session

import (
	"context"
	"errors"

	"deepcode/internal/access/account"
	"deepcode/internal/access/role"
)

var (
	ErrSignUpClosed = errors.New("目前不開放註冊；請向管理員申請帳號")
	ErrSignUpLimit  = errors.New("這個 IP 位址的註冊次數過多；請在 15 分鐘後再試")
)

// SignUpOpen reports whether visitors may create their own account.
func (s *Service) SignUpOpen(ctx context.Context) (bool, error) {
	return s.Store.SignUpOpen(ctx)
}

// SetSignUpOpen opens or closes sign-up; accounts made meanwhile stay.
func (s *Service) SetSignUpOpen(ctx context.Context, open bool) error {
	return s.Store.SetSignUpOpen(ctx, open)
}

// SignUp creates an account with the member role, while sign-up is open, and
// signs it in. Sign-ups count per client address as they start, ten in fifteen
// minutes, so one address can neither fill the user list nor keep the CPU
// hashing passwords; one refused as invalid, before any hashing, is given back.
// ponytail: many addresses are slowed only by PBKDF2, as at sign-in; closing
// sign-up stops them.
func (s *Service) SignUp(ctx context.Context, username, displayName, password, client string) (string, *Principal, error) {
	open, err := s.SignUpOpen(ctx)
	if err != nil {
		return "", nil, err
	}
	if !open {
		return "", nil, ErrSignUpClosed
	}
	c := s.signups.take(client)
	if c == nil {
		return "", nil, ErrSignUpLimit
	}
	_, err = s.Accounts.Create(ctx, account.Input{Username: &username, DisplayName: &displayName, Password: &password, RoleIDs: &[]int64{role.Member}})
	if errors.Is(err, account.ErrInvalid) {
		s.signups.undo(client, c)
	}
	if err != nil {
		return "", nil, err
	}
	// Checked again, as at sign-in, for the hash the session is bound to.
	id, checked, err := s.Accounts.Check(ctx, username, password)
	if err != nil {
		return "", nil, err
	}
	return s.signIn(ctx, id, checked)
}
