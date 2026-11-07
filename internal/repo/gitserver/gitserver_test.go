package gitserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/gitserver/oauth"
	"deepcode/internal/repo/gitserver/store"
)

var servers = []Server{
	{ID: 1, Kind: GitHub, BaseURL: "https://github.com"},
	{ID: 2, Kind: GitLab, BaseURL: "https://code.example.com"},
	{ID: 3, Kind: GitLab, BaseURL: "https://code.example.com/gitlab"},
}

func TestMatch(t *testing.T) {
	ok := map[string]string{ // input → server id:path
		"https://github.com/microsoft/vscode":                "1:microsoft/vscode",
		"https://github.com/microsoft/vscode.git":            "1:microsoft/vscode",
		"https://GitHub.com/karpathy/nanochat/tree/master":   "1:karpathy/nanochat",
		"  celery/celery  ":                                  "1:celery/celery",
		"https://github.com/a-b/.github":                     "1:a-b/.github",
		"https://code.example.com/group/sub/app":             "2:group/sub/app",
		"https://code.example.com/group/app.git":             "2:group/app",
		"https://code.example.com/group/app/-/tree/main/src": "2:group/app",
		"https://code.example.com/gitlab/team/app":           "3:team/app",
		"https://code.example.com/gitlabish/app":             "2:gitlabish/app",
	}
	for in, want := range ok {
		s, path, err := match(servers, in, 0)
		if got := strings.TrimSpace(path); err != nil || got == "" || itoa(s.ID)+":"+got != want {
			t.Errorf("%q: got %d:%s, %v; want %s", in, s.ID, path, err, want)
		}
	}
	if s, path, err := match(servers, "group/sub/app", 2); err != nil || s.ID != 2 || path != "group/sub/app" {
		t.Errorf("bare GitLab path: %d %s %v", s.ID, path, err)
	}
	for _, in := range []string{
		"http://github.com/a/b",           // scheme of no server
		"https://gitlab.com/a/b",          // host of no server
		"https://github.com.evil.com/a/b", // look-alike host
		"https://user:pw@github.com/a/b",  // credentials
		"https://github.com/a",            // no repository
		"https://github.com/a/b?x=1",      // query
		"https://github.com/-a/b",         // option-like owner
		"https://github.com/a/..",         // traversal
		"git@github.com:a/b.git",          // ssh
		"file:///etc/passwd",              // local
		"https://github.com:8443/a/b",     // port
		"--upload-pack=touch /tmp/x",      // argument injection
		"https://github.com/a/b c",        // space
		"",                                // nothing
	} {
		if s, p, err := match(servers, in, 0); err == nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: accepted as %d:%s (%v)", in, s.ID, p, err)
		}
	}
	if _, _, err := match(servers, "https://github.com", 0); err == nil || !strings.Contains(err.Error(), "owner/name") {
		t.Errorf("the base URL alone: %v", err)
	}
	if _, _, err := match(servers, "a/b", 9); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown server id: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestPathsAndLinks: a path a server names must be one clean leaves as it
// is; links to lines follow each kind's format.
func TestPathsAndLinks(t *testing.T) {
	for _, c := range []struct {
		kind, path string
		ok         bool
	}{
		{GitHub, "acme/demo", true},
		{GitLab, "group/sub/app", true},
		{GitHub, "acme/demo/extra", false},
		{GitHub, "acme/demo.git", false},
		{GitHub, "-acme/demo", false},
		{GitLab, "group/../app", false},
		{GitLab, "a b/c", false},
		{GitHub, "acme", false},
		{GitLab, strings.Repeat("g/", 600) + "app", false}, // over 1024 characters
	} {
		if ValidPath(c.kind, c.path) != c.ok {
			t.Errorf("ValidPath(%s, %q) = %v", c.kind, c.path, !c.ok)
		}
	}
	if got := SourceURL(GitLab, "https://code.example.com/g/app", "abc", "src/a b.go", 3, 9); got != "https://code.example.com/g/app/-/blob/abc/src/a%20b.go#L3-9" {
		t.Fatalf("GitLab link: %s", got)
	}
	if got := SourceURL(GitHub, "https://github.com/o/r", "abc", "main.go", 3, 9); got != "https://github.com/o/r/blob/abc/main.go#L3-L9" {
		t.Fatalf("GitHub link: %s", got)
	}
}

func TestRemoteCredentials(t *testing.T) {
	gh := Server{Kind: GitHub, token: "t"}.Remote("https://github.com/o/r")
	gl := Server{Kind: GitLab, token: "t"}.Remote("https://code.example.com/g/r")
	anon := Server{Kind: GitHub}.Remote("https://github.com/o/r")
	if gh.URL != "https://github.com/o/r.git" || gh.Authorization != "Basic eC1hY2Nlc3MtdG9rZW46dA==" ||
		gl.Authorization != "Basic b2F1dGgyOnQ=" || anon.Authorization != "" {
		t.Fatalf("remotes: %+v %+v %+v", gh, gl, anon)
	}
}

// TestStore: tokens are sealed at rest, a default API URL is no override, and
// servers in use keep their address and cannot be deleted.
func TestStore(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, _ := OpenBox("", "test key")
	st := &Service{Store: &store.Store{DB: db}, Box: box}
	str := func(s string) *string { return &s }

	token := func(id int64) string {
		s, err := st.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return s.Token()
	}
	s, err := st.Create(ctx, Input{Name: str("Corp"), Kind: str(GitLab), BaseURL: str("https://code.example.com/"), Token: str("glpat-secret")})
	if err != nil || s.BaseURL != "https://code.example.com" || s.APIURL != "https://code.example.com/api/v4" || !s.HasToken || s.Token() != "" || token(s.ID) != "glpat-secret" {
		t.Fatalf("create: %+v %v", s, err)
	}
	var stored, api string
	db.QueryRow(`SELECT token, api_url FROM git_servers WHERE id = ?`, s.ID).Scan(&stored, &api)
	if stored == "" || strings.Contains(stored, "glpat") || api != "" {
		t.Fatalf("stored token %q, api_url %q", stored, api)
	}
	if _, err := st.Create(ctx, Input{Name: str("corp"), Kind: str(GitLab), BaseURL: str("https://other.example.com")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	for _, base := range []string{"ftp://x", "https://x.example.com?", "https://x.example.com/#"} {
		if _, err := st.Create(ctx, Input{Name: str("Bad"), Kind: str(GitLab), BaseURL: str(base)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad URL %q: %v", base, err)
		}
	}
	if long, err := st.Create(ctx, Input{Name: str(strings.Repeat("長", 64)), Kind: str(GitHub), BaseURL: str("https://ghe.example.org")}); err != nil || st.Delete(ctx, long.ID) != nil {
		t.Fatalf("a name of 64 characters, three bytes each: %v", err)
	}
	// The UI sends back the API URL it was shown; a blank token keeps the token.
	s, err = st.Update(ctx, s.ID, Input{APIURL: str(s.APIURL), Token: str("")})
	if err != nil || token(s.ID) != "glpat-secret" {
		t.Fatalf("update: %+v %v", s, err)
	}
	// The token goes only where it was entered for: moving needs it again.
	for _, in := range []Input{{APIURL: str("https://attacker.example")}, {BaseURL: str("https://attacker.example")}, {Kind: str(GitHub)}} {
		if _, err := st.Update(ctx, s.ID, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("moved the token with %+v: %v", in, err)
		}
	}
	if s, err = st.Update(ctx, s.ID, Input{BaseURL: str("https://git.example.com"), Token: str("glpat-new")}); err != nil || s.APIURL != "https://git.example.com/api/v4" || token(s.ID) != "glpat-new" {
		t.Fatalf("moved with a new token: %+v %v", s, err)
	}
	if s, _ = st.Update(ctx, s.ID, Input{ClearToken: true, BaseURL: str("https://code.example.com")}); s.HasToken {
		t.Fatalf("token not cleared: %+v", s)
	}

	db.Exec(`INSERT INTO repositories(url, owner, name, git_server_id) VALUES ('https://code.example.com/g/r', 'g', 'r', ?)`, s.ID)
	if _, err := st.Update(ctx, s.ID, Input{BaseURL: str("https://moved.example.com")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("moved a server in use: %v", err)
	}
	if err := st.Delete(ctx, s.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted a server in use: %v", err)
	}
	if err := st.Delete(ctx, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted a missing server: %v", err)
	}
	other, _ := OpenBox("", "another key")
	// Under another key, whatever needs the token says so (the API answers 409), and the console still lists it.
	sealed, err := st.Create(ctx, Input{Name: str("Sealed"), Kind: str(GitHub), BaseURL: str("https://ghe.example.com"), Token: str("ghp-secret")})
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO repositories(url, owner, name, git_server_id) VALUES ('https://ghe.example.com/o/r', 'o', 'r', ?)`, sealed.ID)
	var repo int64
	db.QueryRow(`SELECT id FROM repositories WHERE git_server_id = ?`, sealed.ID).Scan(&repo)
	rekeyed := &Service{Store: st.Store, Box: other}
	if _, err := rekeyed.Get(ctx, sealed.ID); !errors.Is(err, ErrSealed) {
		t.Fatalf("get: %v", err)
	}
	if _, _, err := rekeyed.Resolve(ctx, "o/r", sealed.ID); !errors.Is(err, ErrSealed) {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := rekeyed.Remote(ctx, repo); !errors.Is(err, ErrSealed) {
		t.Fatalf("remote: %v", err)
	}
	if list, err := rekeyed.List(ctx); err != nil || len(list) != 3 {
		t.Fatalf("list: %d servers, %v", len(list), err)
	}
}

// TestOAuth: a server authorized through OAuth needs its app's secret and
// takes no pasted token; only the admin who began an authorization finishes
// it, trading the code for tokens kept sealed; a token about to expire is
// refreshed before use, once however many need it; a revoked authorization
// says so; changing the app or going back to a pasted token ends it.
func TestOAuth(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mu sync.Mutex
	refreshes := 0
	gl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f := r.PostForm
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path != "/oauth/token" || f.Get("client_id") != "cid" || f.Get("client_secret") != "cs" || f.Get("redirect_uri") != "https://dc.example/cb":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid_client"}`)
		case f.Get("grant_type") == "authorization_code" && f.Get("code") == "good":
			fmt.Fprint(w, `{"access_token":"at1","refresh_token":"rt1","expires_in":30}`) // within the minute: the next use refreshes
		case f.Get("grant_type") == "refresh_token" && f.Get("refresh_token") == "rt1":
			refreshes++
			fmt.Fprint(w, `{"access_token":"at2","refresh_token":"rt2","expires_in":7200}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"revoked"}`)
		}
	}))
	defer gl.Close()
	box, _ := OpenBox("", "test key")
	sv := &Service{Store: &store.Store{DB: db}, Box: box}
	str := func(s string) *string { return &s }
	app := Input{Name: str("GL"), Kind: str(GitLab), BaseURL: str(gl.URL), ClientID: str("cid")}
	if _, err := sv.Create(ctx, app); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no secret: %v", err)
	}
	app.ClientSecret, app.Token = str("cs"), str("glpat")
	if _, err := sv.Create(ctx, app); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a pasted token too: %v", err)
	}
	app.Token = nil
	s, err := sv.Create(ctx, app)
	var sealed string
	db.QueryRow(`SELECT oauth_client_secret FROM git_servers WHERE id = ?`, s.ID).Scan(&sealed)
	if err != nil || !s.OAuth() || !s.HasClientSecret || s.HasToken || sealed == "" || sealed == "cs" {
		t.Fatalf("create: %+v %q %v", s, sealed, err)
	}

	begin := func() string {
		u, err := sv.Authorize(ctx, s.ID, 7, "https://dc.example/cb")
		page, _ := url.Parse(u)
		if err != nil || page.Path != "/oauth/authorize" || page.Query().Get("client_id") != "cid" {
			t.Fatalf("authorize: %q %v", u, err)
		}
		return page.Query().Get("state")
	}
	if err := sv.Finish(ctx, 8, begin(), "good"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another admin finished: %v", err)
	}
	if err := sv.Finish(ctx, 7, begin(), "bad"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a refused code: %v", err)
	}
	if err := sv.Finish(ctx, 7, begin(), "good"); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO repositories(url, owner, name, git_server_id) VALUES (?, 'g', 'r', ?)`, gl.URL+"/g/r", s.ID)
	var repo int64
	db.QueryRow(`SELECT id FROM repositories WHERE git_server_id = ?`, s.ID).Scan(&repo)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if got, err := sv.Get(ctx, s.ID); err != nil || got.Token() != "at2" {
				t.Errorf("get: %q %v", got.Token(), err)
			}
		})
	}
	wg.Wait()
	remote, err := sv.Remote(ctx, repo)
	if err != nil || refreshes != 1 || remote.Authorization != "Basic b2F1dGgyOmF0Mg==" { // oauth2:at2
		t.Fatalf("remote %+v after %d refreshes: %v", remote, refreshes, err)
	}

	db.Exec(`UPDATE git_servers SET token_expires_at = '2000-01-01T00:00:00Z' WHERE id = ?`, s.ID) // rt2 is refused
	if _, err := sv.Get(ctx, s.ID); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "重新授權") {
		t.Fatalf("a revoked authorization: %v", err)
	}
	was, _ := sv.Store.Get(ctx, s.ID)
	if _, err := sv.Update(ctx, s.ID, Input{Name: str("GL renamed")}); err != nil {
		t.Fatal(err)
	}
	if err := sv.keep(ctx, was, oauth.Tokens{Access: "at3", Refresh: "rt2"}, "https://dc.example/cb"); err != nil {
		t.Fatalf("a rename refused the grant: %v", err)
	}
	was, _ = sv.Store.Get(ctx, s.ID) // as a refresh read it, before the admin's edits below
	if _, err := sv.Update(ctx, s.ID, Input{APIURL: str(gl.URL + "/api")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("the authorization went to another API without the secret: %v", err)
	}
	if _, err := sv.Update(ctx, s.ID, Input{ClearToken: true}); err != nil {
		t.Fatal(err)
	}
	if err := sv.keep(ctx, was, oauth.Tokens{Access: "late"}, "https://dc.example/cb"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a grant landed after the authorization ended: %v", err)
	}
	if err := sv.Decline(7, begin()); err != nil {
		t.Fatal(err)
	}
	if err := sv.Decline(7, "forged"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("declined without a state: %v", err)
	}
	if _, err := sv.Update(ctx, s.ID, Input{ClientID: str("cid2")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another app without its secret: %v", err)
	}
	if s, err = sv.Update(ctx, s.ID, Input{ClientID: str("cid2"), ClientSecret: str("cs2")}); err != nil || s.HasToken {
		t.Fatalf("another app kept the authorization: %+v %v", s, err)
	}
	if s, err = sv.Update(ctx, s.ID, Input{ClientID: str(""), Token: str("glpat")}); err != nil || s.OAuth() || s.HasClientSecret || !s.HasToken {
		t.Fatalf("back to a pasted token: %+v %v", s, err)
	}
	if _, err := sv.Authorize(ctx, s.ID, 7, "https://dc.example/cb"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("authorized a pasted token's server: %v", err)
	}
}
