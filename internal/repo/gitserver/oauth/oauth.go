// Package oauth runs the OAuth 2.0 authorization code flow against a GitHub or
// GitLab server, for the token deepcode reads repositories with: the consent
// page to send the admin to, and what the token endpoint grants for a code or
// a refresh token.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// endpoints are the consent page and token endpoint of a server of kind at
// base, and the scopes that let a token read private repositories: GitHub's
// OAuth apps have no read-only scope for them.
func endpoints(kind, base string) (authorize, token, scope string) {
	if kind == "gitlab" {
		return base + "/oauth/authorize", base + "/oauth/token", "read_api read_repository"
	}
	return base + "/login/oauth/authorize", base + "/login/oauth/access_token", "repo"
}

// URL is the consent page that asks the server's signed-in user to let the
// OAuth app clientID read their repositories, then sends them to redirect
// with a code and state.
func URL(kind, base, clientID, redirect, state string) string {
	authorize, _, scope := endpoints(kind, base)
	return authorize + "?" + url.Values{"client_id": {clientID}, "redirect_uri": {redirect}, "response_type": {"code"},
		"scope": {scope}, "state": {state}}.Encode()
}

// App is an OAuth app registered on a server of Kind at Base, and the
// redirect URI its tokens were granted for (a GitLab refresh repeats it).
type App struct {
	Kind, Base, ClientID, Secret, Redirect string
}

// Tokens are what the token endpoint grants: the access token, the refresh
// token ("" for none) and when the access token expires (zero: never, as with
// a GitHub OAuth app's).
type Tokens struct {
	Access, Refresh string
	Expires         time.Time
}

// Code trades the code the consent page sent back for tokens.
func (a App) Code(ctx context.Context, code string) (Tokens, error) {
	return a.grant(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}})
}

// Refresh trades a refresh token for new tokens; GitLab's refresh token works
// once, and the answer holds the next.
func (a App) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	return a.grant(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

// client follows no redirect: a token endpoint answers, and a redirect would
// carry the client secret elsewhere.
var client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// ErrUnreachable says the token endpoint did not answer, or answered that it
// is down (5xx); the error that wraps it names the endpoint's address, for the
// log only.
var ErrUnreachable = errors.New("the token endpoint did not answer")

// maxToken bounds a granted token: real ones are some dozen bytes, and each is
// stored sealed and sent with every request to the server.
const maxToken = 4096

func (a App) grant(ctx context.Context, form url.Values) (Tokens, error) {
	_, endpoint, _ := endpoints(a.Kind, a.Base)
	form.Set("client_id", a.ClientID)
	form.Set("client_secret", a.Secret)
	form.Set("redirect_uri", a.Redirect)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json") // GitHub answers a form otherwise
	req.Header.Set("User-Agent", "deepcode")
	resp, err := client.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	var body struct {
		Access      string `json:"access_token"`
		Refresh     string `json:"refresh_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	decoded := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) == nil
	switch {
	case resp.StatusCode >= 500: // down, or a proxy in front of it: an outage, not a refusal
		return Tokens{}, fmt.Errorf("%w: it answered %s", ErrUnreachable, resp.Status)
	case decoded && body.Error != "": // GitHub says so with a 200
		return Tokens{}, fmt.Errorf("%s %s", body.Error, body.Description)
	case !decoded || resp.StatusCode != http.StatusOK:
		return Tokens{}, fmt.Errorf("the token endpoint answered %s", resp.Status)
	case body.Access == "" || len(body.Access) > maxToken || len(body.Refresh) > maxToken:
		return Tokens{}, errors.New("the token endpoint granted no usable token")
	}
	t := Tokens{Access: body.Access, Refresh: body.Refresh}
	if body.ExpiresIn > 0 {
		t.Expires = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return t, nil
}
