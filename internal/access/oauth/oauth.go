// Package oauth is deepcode's OAuth 2.1 authorization server for the apps,
// such as MCP clients, that a user lets act for them: an app registers itself
// (RFC 7591) as a public client, a signed-in user approves it in the browser,
// and the app trades the code, with its PKCE verifier, for an access token
// that /mcp takes and a refresh token that renews it. Only a secret's SHA-256
// is stored.
package oauth

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"deepcode/internal/access/oauth/store"
	"deepcode/internal/access/token"
)

type (
	App    = store.App
	Client = store.Client
)

const (
	codeTTL    = 10 * time.Minute
	accessTTL  = time.Hour
	refreshTTL = 30 * 24 * time.Hour // from the last refresh
	// maxUnused bounds the apps that hold no grant (never approved, revoked
	// or lapsed): registering needs no account, so past it the oldest go. A
	// flood of registrations cannot keep a real app out, only make one whose
	// id it pushed out register again.
	maxUnused = 1000
)

// Error is a failure as OAuth names it (RFC 6749 section 5.2): Code is the
// error, the message its description.
type Error struct {
	Code string
	msg  string
}

func (e *Error) Error() string { return e.msg }

func fail(code, msg string) error { return &Error{code, msg} }

// ErrInvalid: an app or redirect URI the browser must not be sent back to.
var ErrInvalid = errors.New("無效的授權請求")

type invalid struct{ msg string }

func (e invalid) Error() string        { return e.msg }
func (e invalid) Is(target error) bool { return target == ErrInvalid }

// Service applies the OAuth rules over the apps and grants in Store, and has
// Tokens issue the access tokens.
type Service struct {
	Store  *store.Store
	Tokens *token.Service
}

// Register stores an app called name (a default when empty) that receives
// codes at redirects.
func (s *Service) Register(ctx context.Context, name string, redirects []string) (Client, error) {
	if len(redirects) == 0 || len(redirects) > 10 {
		return Client{}, fail("invalid_redirect_uri", "redirect_uris must list 1 to 10 URIs")
	}
	for _, r := range redirects {
		if !redirectOK(r) {
			return Client{}, fail("invalid_redirect_uri", "redirect URI not allowed (https, http on a loopback host, or an app scheme): "+r)
		}
	}
	// The name is the app's own say, shown on the consent page: no control,
	// format, line-breaking or default-ignorable characters (a right-to-left
	// override would turn it around, a Hangul filler show as nothing).
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Other_Default_Ignorable_Code_Point,
			unicode.Variation_Selector) {
			return -1
		}
		return r
	}, name))
	if !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) { // blank to the eye
		name = ""
	}
	if name == "" {
		name = "MCP client"
	} else if utf8.RuneCountInString(name) > 100 {
		return Client{}, fail("invalid_client_metadata", "client_name is longer than 100 characters")
	}
	if err := s.Store.Sweep(ctx, maxUnused-1); err != nil {
		return Client{}, err
	}
	c := Client{ID: token.New("dcc_"), Name: name, RedirectURIs: redirects}
	return c, s.Store.AddClient(ctx, c)
}

// Client returns app id when redirect is one of its redirect URIs, else an
// ErrInvalid error: a browser is never sent to a URI no app registered.
func (s *Service) Client(ctx context.Context, id, redirect string) (Client, error) {
	c, err := s.Store.Client(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return c, invalid{"找不到這個應用程式：它可能已過期，請在應用程式裡重新連線"}
	}
	if err == nil && !registered(c.RedirectURIs, redirect) {
		return c, invalid{"這個應用程式沒有登記這個重新導向網址"}
	}
	return c, err
}

// Request is an authorization request, as an app sent it.
type Request struct {
	ResponseType        string `json:"responseType"`
	ClientID            string `json:"clientId"`
	RedirectURI         string `json:"redirectUri"`
	CodeChallenge       string `json:"codeChallenge"`
	CodeChallengeMethod string `json:"codeChallengeMethod"`
	State               string `json:"state"`
}

// Approve answers r for user id, who approved it or not: the URL to send the
// browser back to, holding a code, or an OAuth error the app reads. It fails
// with ErrInvalid instead when r's app or redirect URI is unknown.
func (s *Service) Approve(ctx context.Context, userID int64, r Request, approved bool) (string, error) {
	if _, err := s.Client(ctx, r.ClientID, r.RedirectURI); err != nil {
		return "", err
	}
	answer := func(params map[string]string) string {
		params["state"] = r.State
		return back(r.RedirectURI, params)
	}
	switch {
	case r.ResponseType != "code":
		return answer(map[string]string{"error": "unsupported_response_type"}), nil
	case r.CodeChallengeMethod != "S256" || len(r.CodeChallenge) < 43 || len(r.CodeChallenge) > 128:
		return answer(map[string]string{"error": "invalid_request", "error_description": "PKCE with S256 is required"}), nil
	case !approved:
		return answer(map[string]string{"error": "access_denied"}), nil
	}
	code := token.New("")
	if err := s.Store.AddCode(ctx, token.Digest(code), store.Code{ClientID: r.ClientID, UserID: userID, RedirectURI: r.RedirectURI,
		Challenge: r.CodeChallenge}, codeTTL); err != nil {
		return "", err
	}
	return answer(map[string]string{"code": code}), nil
}

// Tokens is what the token endpoint gives an app.
type Tokens struct {
	Access, Refresh string
	ExpiresIn       int // seconds
}

// Exchange trades app clientID's code, sent back to redirect, for tokens,
// when verifier is the PKCE verifier the code was asked with.
func (s *Service) Exchange(ctx context.Context, clientID, code, redirect, verifier string) (Tokens, error) {
	if err := s.known(ctx, clientID); err != nil {
		return Tokens{}, err
	}
	c, err := s.Store.TakeCode(ctx, token.Digest(code))
	if errors.Is(err, store.ErrNoGrant) || err == nil && (c.ClientID != clientID || c.RedirectURI != redirect || !pkceOK(verifier, c.Challenge)) {
		return Tokens{}, fail("invalid_grant", "the code is unknown, used, expired, or not this app's, redirect URI and PKCE verifier's")
	}
	if err != nil {
		return Tokens{}, err
	}
	return s.issue(ctx, clientID, c.UserID, "")
}

// Refresh trades app clientID's refresh token for new tokens; the old one
// stops working.
func (s *Service) Refresh(ctx context.Context, clientID, refresh string) (Tokens, error) {
	if err := s.known(ctx, clientID); err != nil {
		return Tokens{}, err
	}
	g, err := s.Store.TakeRefresh(ctx, token.Digest(refresh), clientID)
	if errors.Is(err, store.ErrNoGrant) {
		return Tokens{}, fail("invalid_grant", err.Error())
	}
	if err != nil {
		return Tokens{}, err
	}
	return s.issue(ctx, clientID, g.UserID, g.Since)
}

// errGone: the app is not registered (any longer: past maxUnused, the oldest
// unused apps go), which tells an MCP client to register again.
var errGone = fail("invalid_client", "the app is not registered; register it again")

// known fails with errGone unless app clientID is registered.
func (s *Service) known(ctx context.Context, clientID string) error {
	_, err := s.Store.Client(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return errGone
	}
	return err
}

// issue gives app clientID an access and a refresh token for user id, who
// approved it at since ("" now).
func (s *Service) issue(ctx context.Context, clientID string, userID int64, since string) (Tokens, error) {
	c, err := s.Store.Client(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) { // pushed out meanwhile, by a flood of registrations
		return Tokens{}, errGone
	} else if err != nil {
		return Tokens{}, err
	}
	access, err := s.Tokens.Issue(ctx, userID, clientID, c.Name, accessTTL)
	if errors.Is(err, token.ErrNoToken) { // disabled, or deleted, meanwhile
		return Tokens{}, fail("invalid_grant", "the account is disabled")
	}
	if err != nil {
		return Tokens{}, err
	}
	refresh := token.New("dcr_")
	if err := s.Store.AddRefresh(ctx, token.Digest(refresh), clientID, userID, since, refreshTTL); err != nil {
		return Tokens{}, err
	}
	return Tokens{Access: access, Refresh: refresh, ExpiresIn: int(accessTTL.Seconds())}, nil
}

// Apps lists the apps user id lets act for them.
func (s *Service) Apps(ctx context.Context, userID int64) ([]App, error) {
	return s.Store.Apps(ctx, userID)
}

// Revoke stops app clientID acting for user id: its tokens stop working.
func (s *Service) Revoke(ctx context.Context, userID int64, clientID string) error {
	return s.Store.Revoke(ctx, userID, clientID)
}
