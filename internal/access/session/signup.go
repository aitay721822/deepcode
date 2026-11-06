package session

import (
	"context"
	"errors"

	"deepcode/internal/access/account"
	"deepcode/internal/access/role"
)

var (
	ErrSignUpClosed = errors.New("sign-up is closed; ask an administrator for an account")
	ErrSignUpLimit  = errors.New("too many sign-ups from this address; try again in 15 minutes")
)

// SignUpOpen reports whether visitors may create their own account.
func (s *Store) SignUpOpen(ctx context.Context) (bool, error) {
	var open bool
	err := s.DB.QueryRowContext(ctx, `SELECT open FROM signup`).Scan(&open)
	return open, err
}

// SetSignUpOpen opens or closes sign-up; accounts made meanwhile stay.
func (s *Store) SetSignUpOpen(ctx context.Context, open bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE signup SET open = ?`, open)
	return err
}

// SignUp creates an account with the member role, while sign-up is open, and
// signs it in. Sign-ups count per client address as they start, ten in fifteen
// minutes, so one address can neither fill the user list nor keep the CPU
// hashing passwords; one refused as invalid, before any hashing, is given back.
// ponytail: many addresses are slowed only by PBKDF2, as at sign-in; closing
// sign-up stops them.
func (s *Store) SignUp(ctx context.Context, username, displayName, password, client string) (string, *Principal, error) {
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
