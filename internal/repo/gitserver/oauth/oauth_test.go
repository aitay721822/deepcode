package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestURL(t *testing.T) {
	gh, _ := url.Parse(URL("github", "https://github.com", "cid", "https://dc.example/cb", "st"))
	gl, _ := url.Parse(URL("gitlab", "https://code.example.com/gitlab", "cid", "https://dc.example/cb", "st"))
	if gh.Host+gh.Path != "github.com/login/oauth/authorize" || gh.Query().Get("scope") != "repo" || gh.Query().Get("state") != "st" ||
		gl.Path != "/gitlab/oauth/authorize" || gl.Query().Get("scope") != "read_api read_repository" || gl.Query().Get("redirect_uri") != "https://dc.example/cb" ||
		gl.Query().Get("response_type") != "code" || gl.Query().Get("client_id") != "cid" {
		t.Fatalf("%s\n%s", gh, gl)
	}
}

// TestGrant: the token endpoint gets the app's credentials and redirect URI
// with each grant, and its answer comes back as tokens, or as an error
// whether it says so with a 200 (GitHub) or a 400 (GitLab).
func TestGrant(t *testing.T) {
	var forms []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		forms = append(forms, r.PostForm)
		switch {
		case r.URL.Path != "/oauth/token" || r.Header.Get("Accept") != "application/json":
			http.NotFound(w, r)
		case r.PostForm.Get("code") == "bad":
			fmt.Fprint(w, `{"error":"bad_verification_code","error_description":"The code passed is incorrect or expired."}`)
		case r.PostForm.Get("refresh_token") == "used":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
		case r.PostForm.Get("refresh_token") == "down":
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `<html>maintenance</html>`)
		default:
			fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","expires_in":7200,"token_type":"bearer"}`)
		}
	}))
	defer srv.Close()
	app := App{Kind: "gitlab", Base: srv.URL, ClientID: "cid", Secret: "cs", Redirect: "https://dc.example/cb"}
	ctx := context.Background()
	got, err := app.Code(ctx, "good")
	if err != nil || got.Access != "at" || got.Refresh != "rt" || time.Until(got.Expires) < time.Hour || time.Until(got.Expires) > 2*time.Hour {
		t.Fatalf("code: %+v %v", got, err)
	}
	f := forms[0]
	if f.Get("grant_type") != "authorization_code" || f.Get("client_id") != "cid" || f.Get("client_secret") != "cs" || f.Get("redirect_uri") != "https://dc.example/cb" {
		t.Fatalf("form %v", f)
	}
	if _, err := app.Refresh(ctx, "rt"); err != nil || forms[1].Get("grant_type") != "refresh_token" || forms[1].Get("refresh_token") != "rt" {
		t.Fatalf("refresh: %v %v", forms[1], err)
	}
	if _, err := app.Code(ctx, "bad"); err == nil || !strings.Contains(err.Error(), "incorrect or expired") {
		t.Fatalf("a refused code: %v", err)
	}
	if _, err := app.Refresh(ctx, "used"); err == nil || errors.Is(err, ErrUnreachable) {
		t.Fatalf("a refused refresh token: %v", err)
	}
	if _, err := app.Refresh(ctx, "down"); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("a server down for maintenance: %v", err)
	}
}

// TestPending: a state finishes once, for the user who began it, in time.
func TestPending(t *testing.T) {
	var p Pending
	a, b := p.Begin(1, 7, "https://dc.example/cb"), p.Begin(2, 7, "x")
	if a == b || len(a) < 40 {
		t.Fatalf("states %q %q", a, b)
	}
	if _, _, ok := p.Finish(b, 8); ok {
		t.Fatal("another user finished")
	}
	if _, _, ok := p.Finish(b, 7); ok {
		t.Fatal("a state finished after a failed try")
	}
	if server, redirect, ok := p.Finish(a, 7); !ok || server != 1 || redirect != "https://dc.example/cb" {
		t.Fatalf("finish: %d %q %v", server, redirect, ok)
	}
	if _, _, ok := p.Finish(a, 7); ok {
		t.Fatal("a state finished twice")
	}
	old := p.Begin(3, 7, "x")
	p.m[old] = pending{3, 7, "x", time.Now().Add(-ttl - time.Second)}
	if _, _, ok := p.Finish(old, 7); ok {
		t.Fatal("an expired state finished")
	}
}
