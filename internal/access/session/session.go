// Package session signs users in with a password, or up while an
// administrator allows it, and keeps them signed in with an opaque cookie token.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"deepcode/internal/access/account"
	"deepcode/internal/access/role"
	"deepcode/internal/access/session/store"
)

var (
	ErrNoSession = store.ErrNoSession
	ErrThrottled = errors.New("失敗次數過多；請在 15 分鐘後再試")
)

// Principal is a signed-in user and what their roles allow.
type Principal struct {
	UserID      int64
	Username    string
	DisplayName string
	role.Grants
}

// Service signs users in and keeps their sessions in Store. Only a token's
// SHA-256 is stored, so the database alone signs nobody in.
type Service struct {
	Store     *store.Store
	Accounts  *account.Service
	Roles     *role.Service
	TTL       time.Duration
	byUser    limiter // failed sign-ins per username and address; wrong current passwords per user
	byAddress limiter // failed sign-ins per address, whatever the username
	signups   limiter // sign-ups per address
}

func New(st *store.Store, accounts *account.Service, roles *role.Service, ttl time.Duration) *Service {
	return &Service{Store: st, Accounts: accounts, Roles: roles, TTL: ttl,
		byUser: limiter{max: 10, window: 15 * time.Minute}, byAddress: limiter{max: 50, window: 15 * time.Minute},
		signups: limiter{max: 10, window: 15 * time.Minute}}
}

// maxUsername bounds the usernames Login counts failures for: no real one is
// longer, and each counted name is kept in memory for a while.
const maxUsername = 64

// Login checks the password and starts a session. Failures are counted per
// username and client address: after ten in fifteen minutes that pair is
// refused, while the same user elsewhere can still sign in. An address with
// fifty failures in fifteen minutes, across any usernames, is refused too.
// Attempts count from the start, so parallel guesses get no more tries.
// ponytail: an attacker with many addresses is slowed only by PBKDF2.
func (s *Service) Login(ctx context.Context, username, password, client string) (string, *Principal, error) {
	if len(username) > maxUsername {
		return "", nil, account.ErrBadLogin
	}
	key := strings.ToLower(strings.TrimSpace(username)) + "|" + client
	u := s.byUser.take(key)
	if u == nil {
		return "", nil, ErrThrottled
	}
	a := s.byAddress.take(client)
	if a == nil {
		s.byUser.undo(key, u)
		return "", nil, ErrThrottled
	}
	id, checked, err := s.Accounts.Check(ctx, username, password)
	if !errors.Is(err, account.ErrBadLogin) { // only a wrong password stays counted
		s.byUser.undo(key, u)
		s.byAddress.undo(client, a)
	}
	if err != nil {
		return "", nil, err
	}
	return s.signIn(ctx, id, checked)
}

// signIn starts a session for user id, whose password was just checked
// against the hash checked, and returns its token and principal.
func (s *Service) signIn(ctx context.Context, id int64, checked string) (string, *Principal, error) {
	token, err := s.start(ctx, id, checked)
	if errors.Is(err, ErrNoSession) { // disabled or given a new password meanwhile
		err = account.ErrBadLogin
	}
	if err != nil {
		return "", nil, err
	}
	p, err := s.Authenticate(ctx, token)
	return token, p, err
}

// ChangePassword sets user id's password after checking the current one,
// which ends all their sessions, and returns a new session for this browser.
// Wrong current passwords count like failed sign-ins, so a stolen session
// cannot guess the password either.
func (s *Service) ChangePassword(ctx context.Context, id int64, current, next string) (string, error) {
	key := fmt.Sprintf("user %d", id) // sign-in keys hold a "|"
	c := s.byUser.take(key)
	if c == nil {
		return "", ErrThrottled
	}
	checked, err := s.Accounts.ChangePassword(ctx, id, current, next)
	if !errors.Is(err, account.ErrWrongPassword) { // only a wrong password stays counted
		s.byUser.undo(key, c)
	}
	if err != nil {
		return "", err
	}
	return s.start(ctx, id, checked)
}

// start opens a session for user id and returns its token, while the user is
// enabled and still has the password hash checked: a disable or a password
// change committed meanwhile ended their sessions, and must end this one too.
func (s *Service) start(ctx context.Context, id int64, checked string) (string, error) {
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	// Expired sessions are swept here rather than by a timer.
	if err := s.Store.Sweep(ctx); err != nil {
		return "", err
	}
	if err := s.Store.Start(ctx, digest(token), id, checked, s.TTL); err != nil {
		return "", err
	}
	return token, nil
}

// Authenticate returns who token signs in: an enabled user with an unexpired session.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	p := &Principal{}
	var err error
	p.UserID, p.Username, p.DisplayName, err = s.Store.User(ctx, digest(token))
	if err != nil {
		return nil, err
	}
	p.Grants, err = s.Roles.GrantsOf(ctx, p.UserID)
	return p, err
}

// Logout ends the session of token.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.Store.End(ctx, digest(token))
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
