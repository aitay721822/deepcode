package gitserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"deepcode/internal/repo/gitserver/oauth"
	"deepcode/internal/repo/gitserver/store"
)

// Authorize begins authorizing server id through OAuth for user, an admin:
// the URL of the server's consent page, which sends them back to redirect,
// this installation's callback, with a code for Finish.
func (sv *Service) Authorize(ctx context.Context, id, user int64, redirect string) (string, error) {
	s, err := sv.view(ctx, id)
	if err != nil {
		return "", err
	}
	if !s.OAuth() {
		return "", invalid("這個 Git 伺服器沒有設定 OAuth 應用程式")
	}
	return oauth.URL(s.Kind, s.BaseURL, s.ClientID, redirect, sv.pending.Begin(id, user, redirect)), nil
}

// Finish ends an authorization user began with state: it trades the code the
// consent page sent back for the server's tokens and keeps them, sealed.
func (sv *Service) Finish(ctx context.Context, user int64, state, code string) error {
	id, redirect, ok := sv.pending.Finish(state, user)
	if !ok {
		return errStale
	}
	r, err := sv.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	s := fromRow(r).effective()
	if !s.OAuth() { // switched to a pasted token meanwhile
		return invalid("這個 Git 伺服器已不使用 OAuth")
	}
	app, err := sv.app(s, redirect)
	if err != nil {
		return err
	}
	t, err := app.Code(ctx, code)
	if err != nil {
		log.Printf("gitserver: authorizing %s: %v", s.Name, err)
		return invalid("%s 沒有核發 Token：%s", s.Name, reason(err))
	}
	return sv.keep(ctx, r, t, redirect)
}

var errStale = invalid("授權已逾時或無效，請重新授權")

// Decline ends an authorization user began with state that the consent page
// turned down. Without one, errStale: what the page said is then anyone's to
// write, so it is not shown.
func (sv *Service) Decline(user int64, state string) error {
	if _, _, ok := sv.pending.Finish(state, user); !ok {
		return errStale
	}
	return nil
}

// expiring reports whether s's token is granted through OAuth and expires
// within a minute, and can be refreshed.
func (s Server) expiring() bool {
	at, err := time.Parse(time.RFC3339, s.oauth.Expires)
	return s.OAuth() && s.oauth.Refresh != "" && err == nil && time.Until(at) < time.Minute
}

// refresh trades server id's refresh token for new tokens and returns the
// server with them. One refresh runs at a time: GitLab's refresh token works
// once, so of two at once one would fail; the second finds the server
// refreshed and returns it as it is.
func (sv *Service) refresh(ctx context.Context, id int64) (Server, error) {
	sv.refreshing.Lock()
	defer sv.refreshing.Unlock()
	r, err := sv.Store.Get(ctx, id)
	if err != nil {
		return Server{}, err
	}
	s := fromRow(r).effective()
	if !s.expiring() {
		return s, nil
	}
	refresh, err := sv.Box.Open(s.oauth.Refresh)
	if err != nil {
		return Server{}, fmt.Errorf("Git 伺服器 %s：%w", s.Name, err)
	}
	app, err := sv.app(s, s.oauth.Redirect)
	if err != nil {
		return Server{}, err
	}
	t, err := app.Refresh(ctx, refresh)
	if err != nil {
		log.Printf("gitserver: refreshing the token of %s: %v", s.Name, err)
		if errors.Is(err, oauth.ErrUnreachable) {
			return Server{}, failure{ErrOutage, fmt.Sprintf("Git 伺服器 %s 的 OAuth 權杖端點沒有回應，請稍後再試", s.Name)}
		}
		return Server{}, failure{ErrInvalid, fmt.Sprintf("Git 伺服器 %s 的 OAuth 授權已失效（%s），請管理員重新授權", s.Name, reason(err))}
	}
	if t.Refresh == "" { // GitHub may keep the one it had
		t.Refresh = refresh
	}
	if err := sv.keep(ctx, r, t, s.oauth.Redirect); err != nil {
		return Server{}, err
	}
	r, err = sv.Store.Get(ctx, id)
	return fromRow(r).effective(), err
}

// app is the OAuth app s is authorized with, its secret unsealed.
func (sv *Service) app(s Server, redirect string) (oauth.App, error) {
	secret, err := sv.Box.Open(s.oauth.ClientSecret)
	if err != nil {
		return oauth.App{}, fmt.Errorf("Git 伺服器 %s：%w", s.Name, err)
	}
	return oauth.App{Kind: s.Kind, Base: s.BaseURL, ClientID: s.ClientID, Secret: secret, Redirect: redirect}, nil
}

// keep stores the tokens t granted for redirect to the server as it was read
// before asking, sealed. One edited meanwhile keeps its own: its address,
// app or authorization is no longer the one these were granted for.
func (sv *Service) keep(ctx context.Context, was store.Server, t oauth.Tokens, redirect string) error {
	expires := ""
	if !t.Expires.IsZero() {
		expires = t.Expires.UTC().Format(time.RFC3339)
	}
	err := sv.Store.SetTokens(ctx, was, sv.Box.Seal(t.Access), sv.Box.Seal(t.Refresh), redirect, expires)
	if errors.Is(err, store.ErrNotFound) {
		return failure{ErrConflict, "授權期間這個伺服器的 OAuth 設定已變更，請重新授權"}
	}
	return err
}

// reason is what a token endpoint's refusal says, short enough to show. One
// that did not answer shows only that: the error names its address.
func reason(err error) string {
	if errors.Is(err, oauth.ErrUnreachable) {
		return "權杖端點沒有回應或暫時無法使用，請稍後再試"
	}
	msg := []rune(err.Error())
	if len(msg) > 200 {
		msg = append(msg[:200], '…')
	}
	return string(msg)
}
