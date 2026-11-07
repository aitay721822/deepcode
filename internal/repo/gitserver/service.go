package gitserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver/oauth"
	"deepcode/internal/repo/gitserver/store"
)

// Service keeps the servers in the store, tokens and OAuth secrets sealed by
// Box, and authorizes them with OAuth.
type Service struct {
	Store *store.Store
	Box   *Box

	pending    oauth.Pending // authorizations begun
	refreshing sync.Mutex    // ponytail: one token refresh at a time for all servers; per-server locks if a slow one stalls the rest
}

// fromRow is a server as stored: APIURL holds only an override.
func fromRow(r store.Server) Server {
	return Server{ID: r.ID, Name: r.Name, Kind: r.Kind, BaseURL: r.BaseURL, APIURL: r.APIURL, HasToken: r.Token != "",
		RepositoryCount: r.RepositoryCount, CreatedAt: r.CreatedAt, ClientID: r.ClientID, HasClientSecret: r.ClientSecret != "",
		sealed: r.Token, oauth: r.OAuth}
}

// row is s as stored, its token and secrets sealed.
func (s Server) row() store.Server {
	o := s.oauth
	o.ClientID = s.ClientID
	return store.Server{ID: s.ID, Name: s.Name, Kind: s.Kind, BaseURL: s.BaseURL, APIURL: s.APIURL, Token: s.sealed,
		CreatedAt: s.CreatedAt, RepositoryCount: s.RepositoryCount, OAuth: o}
}

// effective fills in the kind's default API URL.
func (s Server) effective() Server {
	if s.APIURL == "" {
		s.APIURL = defaultAPI(s.Kind, s.BaseURL)
	}
	return s
}

// List returns every server, oldest first; tokens stay sealed.
func (sv *Service) List(ctx context.Context) ([]Server, error) {
	rows, err := sv.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Server, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r).effective()
	}
	return out, nil
}

// Get returns server id with its token unsealed.
func (sv *Service) Get(ctx context.Context, id int64) (Server, error) {
	r, err := sv.Store.Get(ctx, id)
	if err != nil {
		return Server{}, err
	}
	return sv.unseal(ctx, fromRow(r).effective())
}

// view returns server id as the console shows it: its token stays sealed.
func (sv *Service) view(ctx context.Context, id int64) (Server, error) {
	r, err := sv.Store.Get(ctx, id)
	if err != nil {
		return Server{}, err
	}
	return fromRow(r).effective(), nil
}

// unseal opens s's token, first refreshing one granted through OAuth that is
// about to expire.
func (sv *Service) unseal(ctx context.Context, s Server) (Server, error) {
	if s.expiring() {
		var err error
		if s, err = sv.refresh(ctx, s.ID); err != nil {
			return Server{}, err
		}
	}
	token, err := sv.Box.Open(s.sealed)
	if err != nil {
		return Server{}, fmt.Errorf("Git 伺服器 %s：%w", s.Name, err)
	}
	s.token = token
	return s, nil
}

func (sv *Service) Create(ctx context.Context, in Input) (Server, error) {
	var s Server
	token, secret, err := in.apply(&s)
	if err != nil {
		return Server{}, err
	}
	s.sealed, s.oauth.ClientSecret = sv.Box.Seal(token), sv.Box.Seal(secret)
	id, err := sv.Store.Create(ctx, s.row())
	if err != nil {
		return Server{}, duplicate(err)
	}
	return sv.view(ctx, id)
}

// Update changes server id. The kind and base URL of a server that
// repositories use are fixed, since their URLs and links depend on them. A
// stored token or OAuth secret only goes where it was entered for: changing
// the base or API URL needs it entered again (or the token cleared). An OAuth
// authorization holds for its app at its address: changing either, or
// switching between OAuth and a pasted token, ends it.
func (sv *Service) Update(ctx context.Context, id int64, in Input) (Server, error) {
	err := sv.Store.Update(ctx, id, func(r store.Server) (store.Server, error) {
		s := fromRow(r)
		was := s.effective()
		token, secret, err := in.apply(&s)
		if err != nil {
			return store.Server{}, err
		}
		if s.RepositoryCount > 0 && (s.Kind != was.Kind || !strings.EqualFold(s.BaseURL, was.BaseURL)) {
			return store.Server{}, failure{ErrConflict, "已有 Repository 來自這個伺服器，因此不能變更它的類型與基底網址"}
		}
		now := s.effective()
		moved := now.BaseURL != was.BaseURL || now.APIURL != was.APIURL
		switch {
		case s.OAuth() && secret == "" && (moved || s.ClientID != was.ClientID):
			return store.Server{}, invalid("變更伺服器位址或 OAuth 應用程式時必須重新輸入 Client Secret")
		case !s.OAuth() && !was.OAuth() && s.sealed != "" && token == "" && !in.ClearToken && moved:
			return store.Server{}, invalid("變更伺服器位址時必須重新輸入存取 Token")
		}
		if in.ClearToken || s.ClientID != was.ClientID || s.OAuth() && moved {
			s.sealed, s.oauth.Refresh, s.oauth.Redirect, s.oauth.Expires = "", "", "", ""
		}
		if token != "" {
			s.sealed = sv.Box.Seal(token)
		}
		switch {
		case !s.OAuth():
			s.oauth.ClientSecret = ""
		case secret != "":
			s.oauth.ClientSecret = sv.Box.Seal(secret)
		}
		return s.row(), nil
	})
	if err != nil {
		return Server{}, duplicate(err)
	}
	return sv.view(ctx, id)
}

// Delete removes server id unless repositories come from it.
func (sv *Service) Delete(ctx context.Context, id int64) error {
	deleted, err := sv.Store.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		if _, err := sv.Store.Get(ctx, id); err != nil {
			return err
		}
		return failure{ErrConflict, "已有 Repository 來自這個伺服器，因此不能刪除"}
	}
	return nil
}

// Remote is how git fetches repository repoID from its server.
func (sv *Service) Remote(ctx context.Context, repoID int64) (git.Remote, error) {
	webURL, r, err := sv.Store.Remote(ctx, repoID)
	if errors.Is(err, store.ErrNotFound) {
		return git.Remote{}, fmt.Errorf("repository %d or its git server is gone", repoID)
	} else if err != nil {
		return git.Remote{}, err
	}
	s, err := sv.unseal(ctx, fromRow(r))
	if err != nil {
		return git.Remote{}, err
	}
	return s.Remote(webURL), nil
}

// duplicate tells the user that another server has the name or base URL.
func duplicate(err error) error {
	if errors.Is(err, store.ErrDuplicate) {
		return failure{ErrConflict, "已有相同名稱或基底網址的 Git 伺服器"}
	}
	return err
}
