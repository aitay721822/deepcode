package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"deepcode/internal/access/account"
	accountstore "deepcode/internal/access/account/store"
	"deepcode/internal/access/oauth/store"
	"deepcode/internal/access/role"
	rolestore "deepcode/internal/access/role/store"
	"deepcode/internal/access/token"
	tokenstore "deepcode/internal/access/token/store"
	"deepcode/internal/platform/storage"
)

// TestRedirects: what an app may register, and which URIs then match.
func TestRedirects(t *testing.T) {
	for uri, ok := range map[string]bool{
		"https://app.example/cb": true, "http://127.0.0.1:33418/": true, "http://localhost:9/callback": true, "http://[::1]:5/cb": true,
		"cursor://anysphere.cursor-mcp/oauth/callback": true, "com.example.app:/callback": true, "vscode://ext.id/cb": true,
		"http://localhost@evil.example/cb": false, "http://127.0.0.1.evil.example/cb": false, "ms-msdt:/id x": false, "search-ms:query=x": false, "search-ms://x.y/q": false, "steam://run/1": false, "smb://files.example/s": false,
		"http://app.example/cb": false, "https://app.example/cb#x": false, "https://app.example/cb#": false, "javascript:alert(1)": false,
		"data:text/html,x": false, "JavaScript:alert(1)": false, "file:///etc/passwd": false, "/relative": false, "https:///nohost": false,
	} {
		if redirectOK(uri) != ok {
			t.Errorf("redirectOK(%q) = %v", uri, !ok)
		}
	}
	uris := []string{"http://127.0.0.1:1000/cb", "https://app.example/cb"}
	for uri, ok := range map[string]bool{
		"http://127.0.0.1:2000/cb": true, "https://app.example/cb": true, "http://127.0.0.1:2000/other": false,
		"https://app.example:444/cb": false, "http://localhost:1000/cb": false, "https://app.example/cb?x=1": false,
	} {
		if registered(uris, uri) != ok {
			t.Errorf("registered(%q) = %v", uri, !ok)
		}
	}
}

func setup(t *testing.T) (*Service, int64) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, _, err := (&account.Service{Store: &accountstore.Store{DB: db}}).Bootstrap(ctx, "password-0"); err != nil {
		t.Fatal(err)
	}
	var admin int64
	if err := db.QueryRow(`SELECT id FROM users`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	tokens := &token.Service{Store: &tokenstore.Store{DB: db}, Roles: &role.Service{Store: &rolestore.Store{DB: db}}}
	return &Service{Store: &store.Store{DB: db}, Tokens: tokens}, admin
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// TestFlow: a code is given only for an S256 challenge and the user's
// approval, works once, with its verifier and redirect URI; a refresh token
// works once and renews the access token; revoking stops both.
func TestFlow(t *testing.T) {
	s, user := setup(t)
	ctx := context.Background()
	if _, err := s.Register(ctx, "x", []string{"http://evil.example/cb"}); err == nil {
		t.Fatal("registered a plain-http redirect")
	}
	c, err := s.Register(ctx, "‮Age​nt\n", []string{"http://127.0.0.1:7000/cb"}) // a name turned around, or hidden in
	if err != nil || c.Name != "Agent" {
		t.Fatalf("%+v %v", c, err)
	}
	if blank, err := s.Register(ctx, "ㅤ⠀ ", []string{"http://127.0.0.1:7000/cb"}); err != nil || blank.Name != "MCP client" {
		t.Fatalf("a name blank to the eye: %+v %v", blank, err)
	}
	if split, err := s.Register(ctx, "A B ️", []string{"http://127.0.0.1:7000/cb"}); err != nil || split.Name != "AB" {
		t.Fatalf("a name broken over lines: %+v %v", split, err)
	}
	verifier := strings.Repeat("v", 43)
	r := Request{ResponseType: "code", ClientID: c.ID, RedirectURI: "http://127.0.0.1:7001/cb", CodeChallenge: challenge(verifier),
		CodeChallengeMethod: "S256", State: "st"}
	query := func(approved bool, r Request) url.Values {
		t.Helper()
		to, err := s.Approve(ctx, user, r, approved)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(to)
		if u.Host != "127.0.0.1:7001" || u.Query().Get("state") != "st" {
			t.Fatalf("sent back to %s", to)
		}
		return u.Query()
	}
	if q := query(false, r); q.Get("error") != "access_denied" || q.Get("code") != "" {
		t.Errorf("declined: %v", q)
	}
	plain := r
	plain.CodeChallengeMethod = "plain"
	if q := query(true, plain); q.Get("error") != "invalid_request" {
		t.Errorf("plain PKCE: %v", q)
	}
	for _, n := range []int{42, 129} {
		odd := r
		odd.CodeChallenge = strings.Repeat("c", n)
		if q := query(true, odd); q.Get("error") != "invalid_request" || q.Get("code") != "" {
			t.Errorf("a challenge of %d characters: %v", n, q)
		}
	}
	other := r
	other.RedirectURI = "https://elsewhere.example/cb"
	if _, err := s.Approve(ctx, user, other, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unregistered redirect: %v", err)
	}
	code := query(true, r).Get("code")
	if _, err := s.Exchange(ctx, c.ID, code, r.RedirectURI, strings.Repeat("w", 43)); err == nil {
		t.Fatal("a wrong verifier got tokens")
	}
	other2, err := s.Register(ctx, "Other", []string{r.RedirectURI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exchange(ctx, other2.ID, query(true, r).Get("code"), r.RedirectURI, verifier); err == nil {
		t.Fatal("another app traded the code")
	}
	for _, redirect := range []string{"http://127.0.0.1:7000/cb", ""} { // another URI the app registered, or none
		if _, err := s.Exchange(ctx, c.ID, query(true, r).Get("code"), redirect, verifier); err == nil {
			t.Fatalf("a code traded at redirect %q", redirect)
		}
	}
	var e *Error
	for _, v := range []string{"v", strings.Repeat("v", 129)} { // a verifier too short or too long, asked with its challenge
		odd := r
		odd.CodeChallenge = challenge(v)
		if _, err := s.Exchange(ctx, c.ID, query(true, odd).Get("code"), r.RedirectURI, v); !errors.As(err, &e) || e.Code != "invalid_grant" {
			t.Errorf("a verifier of %d characters: %v", len(v), err)
		}
	}
	code = query(true, r).Get("code") // the failed exchanges used theirs up
	got, err := s.Exchange(ctx, c.ID, code, r.RedirectURI, verifier)
	if err != nil || got.Access == "" || got.Refresh == "" {
		t.Fatalf("exchange: %+v %v", got, err)
	}
	if _, err := s.Exchange(ctx, c.ID, code, r.RedirectURI, verifier); err == nil {
		t.Fatal("a code worked twice")
	}
	if p, _, err := s.Tokens.Authenticate(ctx, got.Access); err != nil || p.UserID != user || !p.Can(role.RepoRead) {
		t.Fatalf("the access token: %+v %v", p, err)
	}
	next, err := s.Refresh(ctx, c.ID, got.Refresh)
	if err != nil || next.Refresh == got.Refresh {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := s.Refresh(ctx, c.ID, got.Refresh); err == nil {
		t.Fatal("a refresh token worked twice")
	}
	if _, err := s.Refresh(ctx, other2.ID, next.Refresh); !errors.As(err, &e) || e.Code != "invalid_grant" {
		t.Fatalf("another app used the refresh token: %v", err)
	}
	if next, err = s.Refresh(ctx, c.ID, next.Refresh); err != nil {
		t.Fatalf("the other app's try took the refresh token: %v", err)
	}
	if _, err := s.Refresh(ctx, "dcc_gone", next.Refresh); !errors.As(err, &e) || e.Code != "invalid_client" {
		t.Errorf("an app not registered: %v", err)
	}
	if apps, err := s.Apps(ctx, user); err != nil || len(apps) != 1 || apps[0].Name != "Agent" {
		t.Fatalf("apps: %+v %v", apps, err)
	}
	pending := query(true, r).Get("code")
	if err := s.Revoke(ctx, user, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exchange(ctx, c.ID, pending, r.RedirectURI, verifier); err == nil {
		t.Error("a code given before the revoke still worked")
	}
	if _, _, err := s.Tokens.Authenticate(ctx, next.Access); !errors.Is(err, token.ErrNoToken) {
		t.Errorf("a revoked app's token: %v", err)
	}
	if _, err := s.Refresh(ctx, c.ID, next.Refresh); err == nil {
		t.Error("a revoked app refreshed")
	}
}

// TestExpiry: an expired code, refresh token, access token or personal token
// works no more, and neither the app nor the personal token is listed.
func TestExpiry(t *testing.T) {
	s, user := setup(t)
	ctx := context.Background()
	redirect := "http://127.0.0.1:7000/cb"
	c, err := s.Register(ctx, "Agent", []string{redirect})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	code := func() string {
		t.Helper()
		to, err := s.Approve(ctx, user, Request{ResponseType: "code", ClientID: c.ID, RedirectURI: redirect,
			CodeChallenge: challenge(verifier), CodeChallengeMethod: "S256"}, true)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(to)
		return u.Query().Get("code")
	}
	expire := func(table string) {
		t.Helper()
		if _, err := s.Store.DB.Exec(`UPDATE ` + table + ` SET expires_at = datetime('now', '-1 second')`); err != nil {
			t.Fatal(err)
		}
	}
	var e *Error
	stale := code()
	expire("oauth_codes")
	if _, err := s.Exchange(ctx, c.ID, stale, redirect, verifier); !errors.As(err, &e) || e.Code != "invalid_grant" {
		t.Errorf("an expired code: %v", err)
	}
	got, err := s.Exchange(ctx, c.ID, code(), redirect, verifier)
	if err != nil {
		t.Fatal(err)
	}
	personal, _, err := s.Tokens.Create(ctx, user, "cli", 30)
	if err != nil {
		t.Fatal(err)
	}
	expire("oauth_refresh_tokens")
	expire("api_tokens")
	if _, err := s.Refresh(ctx, c.ID, got.Refresh); !errors.As(err, &e) || e.Code != "invalid_grant" {
		t.Errorf("an expired refresh token: %v", err)
	}
	for _, secret := range []string{got.Access, personal} {
		if _, _, err := s.Tokens.Authenticate(ctx, secret); !errors.Is(err, token.ErrNoToken) {
			t.Errorf("an expired token: %v", err)
		}
	}
	if list, err := s.Tokens.List(ctx, user); err != nil || len(list) != 0 {
		t.Errorf("personal tokens: %+v %v", list, err)
	}
	if apps, err := s.Apps(ctx, user); err != nil || len(apps) != 0 {
		t.Errorf("apps: %+v %v", apps, err)
	}
}

// TestDisabled: a disabled user's app can neither use nor renew its tokens.
func TestDisabled(t *testing.T) {
	s, user := setup(t)
	ctx := context.Background()
	c, err := s.Register(ctx, "Agent", []string{"http://127.0.0.1:7000/cb"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	to, err := s.Approve(ctx, user, Request{ResponseType: "code", ClientID: c.ID, RedirectURI: "http://127.0.0.1:7000/cb",
		CodeChallenge: challenge(verifier), CodeChallengeMethod: "S256"}, true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	got, err := s.Exchange(ctx, c.ID, u.Query().Get("code"), "http://127.0.0.1:7000/cb", verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, user); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Tokens.Authenticate(ctx, got.Access); !errors.Is(err, token.ErrNoToken) {
		t.Errorf("a disabled user's token: %v", err)
	}
	if _, err := s.Refresh(ctx, c.ID, got.Refresh); err == nil {
		t.Error("a disabled user's app refreshed")
	}
}

// TestEviction: past the bound, the oldest apps holding no grant are
// dropped, never one holding a refresh token, a code or an access token; an
// unused app is kept however old while under the bound; an app dropped
// meanwhile gets invalid_client.
func TestEviction(t *testing.T) {
	s, user := setup(t)
	ctx := context.Background()
	for _, id := range []string{"held", "coded", "tokened", "old", "mid", "new"} {
		if err := s.Store.AddClient(ctx, Client{ID: id, Name: id, RedirectURIs: []string{"https://app.example/cb"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.AddRefresh(ctx, "hash", "held", user, "", refreshTTL); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.AddCode(ctx, "code", store.Code{ClientID: "coded", UserID: user, RedirectURI: "https://app.example/cb"}, codeTTL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tokens.Issue(ctx, user, "tokened", "tokened", accessTTL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE oauth_clients SET created_at = datetime('now', '-400 days') WHERE id IN ('old', 'mid')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Sweep(ctx, 3); err != nil { // old, mid and new are unused, and none past the bound
		t.Fatal(err)
	}
	if _, err := s.Store.Client(ctx, "old"); err != nil {
		t.Fatalf("an old unused app was dropped under the bound: %v", err)
	}
	if err := s.Store.Sweep(ctx, 2); err != nil {
		t.Fatal(err)
	}
	for id, kept := range map[string]bool{"held": true, "coded": true, "tokened": true, "old": false, "mid": true, "new": true} {
		if _, err := s.Store.Client(ctx, id); (err == nil) != kept {
			t.Errorf("%s: %v", id, err)
		}
	}
	var e *Error
	if _, err := s.issue(ctx, "old", user, ""); !errors.As(err, &e) || e.Code != "invalid_client" {
		t.Errorf("a dropped app's tokens: %v", err)
	}
}
