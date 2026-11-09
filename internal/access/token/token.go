// Package token issues the bearer tokens /mcp takes: personal access tokens a
// user creates and revokes, and the access tokens package oauth gives the apps
// a user lets act for them. A token acts with its user's roles, and stops with
// the account. Only a token's SHA-256 is stored.
package token

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/access/token/store"
)

type Token = store.Token

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = store.ErrNotFound
	ErrInvalid  = errors.New("無效的權杖設定")
	ErrNoToken  = store.ErrNoToken // the bearer is signed in by nothing
)

type failure struct {
	kind error
	msg  string
}

func (f failure) Error() string        { return f.msg }
func (f failure) Is(target error) bool { return target == f.kind }

// maxDays bounds a personal token's life; 0 asks for one until revoked.
const maxDays = 366

// Service applies the token rules over the tokens in Store.
type Service struct {
	Store *store.Store
	Roles *role.Service
}

// Create gives user id a personal token called name, lasting days (0: until
// revoked), and returns its secret, shown this once, and the token.
func (s *Service) Create(ctx context.Context, userID int64, name string, days int) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", Token{}, failure{ErrInvalid, "名稱長度必須為 1 到 100 個字元"}
	}
	if days < 0 || days > maxDays {
		return "", Token{}, failure{ErrInvalid, "有效天數必須為 0（不過期）到 366"}
	}
	secret := New("dcp_")
	t, err := s.Store.Add(ctx, userID, "", name, Digest(secret), time.Duration(days)*24*time.Hour)
	return secret, t, err
}

// Issue gives app clientID an access token for user id, lasting ttl, called
// name, and returns its secret.
func (s *Service) Issue(ctx context.Context, userID int64, clientID, name string, ttl time.Duration) (string, error) {
	if err := s.Store.Sweep(ctx); err != nil { // expired tokens are swept here rather than by a timer
		return "", err
	}
	secret := New("dca_")
	_, err := s.Store.Add(ctx, userID, clientID, name, Digest(secret), ttl)
	return secret, err
}

// List returns user id's personal tokens, newest first.
func (s *Service) List(ctx context.Context, userID int64) ([]Token, error) {
	return s.Store.Personal(ctx, userID)
}

// Revoke deletes user id's personal token id.
func (s *Service) Revoke(ctx context.Context, userID, id int64) error {
	return s.Store.Delete(ctx, userID, id)
}

// Authenticate returns who secret signs in, and when it expires (zero: until
// revoked), or ErrNoToken.
func (s *Service) Authenticate(ctx context.Context, secret string) (*session.Principal, time.Time, error) {
	h, err := s.Store.Holder(ctx, Digest(secret))
	if err != nil {
		return nil, time.Time{}, err
	}
	var expires time.Time
	if h.ExpiresAt != nil {
		if expires, err = time.Parse(time.DateTime, *h.ExpiresAt); err != nil { // SQLite's UTC datetime()
			return nil, time.Time{}, err
		}
	}
	p := &session.Principal{UserID: h.UserID, Username: h.Username, DisplayName: h.DisplayName}
	p.Grants, err = s.Roles.GrantsOf(ctx, h.UserID)
	return p, expires, err
}

// New returns a fresh secret: prefix, then 32 random bytes. The prefix says
// what a leaked secret is (secret scanners match it); the bytes make it.
func New(prefix string) string {
	raw := make([]byte, 32)
	rand.Read(raw)
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

// Digest is what the database keeps of secret.
func Digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
