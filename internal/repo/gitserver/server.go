// Package gitserver keeps the git servers repositories are imported from:
// GitHub, GitLab, or a self-hosted instance of either, each with an optional
// access token sealed at rest, pasted or granted through OAuth, and knows
// their URLs: repository pages, clone URLs and links to lines.
package gitserver

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver/secret"
	"deepcode/internal/repo/gitserver/store"
)

const (
	GitHub = "github"
	GitLab = "gitlab"
)

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = store.ErrNotFound
	ErrInvalid  = errors.New("無效的 Git 伺服器")
	ErrConflict = errors.New("Git 伺服器衝突") // a duplicate, or in use by repositories
	ErrSealed   = secret.ErrSealed
	ErrOutage   = errors.New("Git 伺服器沒有回應") // its OAuth token endpoint did not answer; the authorization stands
)

// Box seals the tokens and OAuth secrets; OpenBox opens the one this
// installation's key makes.
type Box = secret.Box

var OpenBox = secret.OpenBox

type Server struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	BaseURL         string `json:"baseUrl"`
	APIURL          string `json:"apiUrl"`   // the override, else the kind's default
	HasToken        bool   `json:"hasToken"` // with OAuth: authorized
	RepositoryCount int    `json:"repositoryCount"`
	CreatedAt       string `json:"createdAt"`
	ClientID        string `json:"clientId"` // the OAuth app the token is granted to, "" when it is pasted
	HasClientSecret bool   `json:"hasClientSecret"`

	sealed string      // the token as stored
	token  string      // the token, once unsealed
	oauth  store.OAuth // the app's secret and the refresh token, sealed
}

// OAuth reports whether the server's token is granted through OAuth.
func (s Server) OAuth() bool { return s.ClientID != "" }

// Token is the access token, "" for anonymous access or before it is unsealed.
func (s Server) Token() string { return s.token }

// WebURL is the page of the repository at path on this server.
func (s Server) WebURL(path string) string { return s.BaseURL + "/" + path }

// Remote is how git fetches the repository at webURL: its clone URL, with the
// token as Basic credentials under the user name each kind expects.
func (s Server) Remote(webURL string) git.Remote {
	r := git.Remote{URL: webURL + ".git"}
	if s.token != "" {
		user := "oauth2"
		if s.Kind == GitHub {
			user = "x-access-token"
		}
		r.Authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+s.token))
	}
	return r
}

// SourceURL links lines start to end of path at commit sha on webURL, the
// page of a repository on a server of kind.
func SourceURL(kind, webURL, sha, path string, start, end int) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	if kind == GitLab {
		return fmt.Sprintf("%s/-/blob/%s/%s#L%d-%d", webURL, sha, strings.Join(parts, "/"), start, end)
	}
	return fmt.Sprintf("%s/blob/%s/%s#L%d-L%d", webURL, sha, strings.Join(parts, "/"), start, end)
}

// defaultAPI is the REST API root of a server of kind at base.
func defaultAPI(kind, base string) string {
	switch {
	case kind == GitLab:
		return base + "/api/v4"
	case strings.EqualFold(base, "https://github.com"):
		return "https://api.github.com"
	default: // GitHub Enterprise Server
		return base + "/api/v3"
	}
}

// Input creates or changes a server; nil fields keep their value.
type Input struct {
	Name         *string `json:"name"`
	Kind         *string `json:"kind"`
	BaseURL      *string `json:"baseUrl"`
	APIURL       *string `json:"apiUrl"`
	Token        *string `json:"token"`        // "" keeps the current token
	ClearToken   bool    `json:"clearToken"`   // with OAuth: ends the authorization
	ClientID     *string `json:"clientId"`     // the OAuth app; "" to paste a token instead
	ClientSecret *string `json:"clientSecret"` // "" keeps the current secret
}

// apply validates in and applies it to s; token and clientSecret are the new
// ones, if any.
func (in Input) apply(s *Server) (token, clientSecret string, err error) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	oldDefault := defaultAPI(s.Kind, s.BaseURL)
	set(&s.Name, in.Name)
	set(&s.Kind, in.Kind)
	if in.BaseURL != nil {
		s.BaseURL = strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
	}
	if in.APIURL != nil {
		s.APIURL = strings.TrimRight(strings.TrimSpace(*in.APIURL), "/")
	}
	set(&token, in.Token)
	set(&s.ClientID, in.ClientID)
	set(&clientSecret, in.ClientSecret)
	switch {
	case s.Name == "" || utf8.RuneCountInString(s.Name) > 64: // characters, not bytes
		return "", "", invalid("名稱必須為 1 到 64 個字元")
	case s.Kind != GitHub && s.Kind != GitLab:
		return "", "", invalid("類型必須是 github 或 gitlab")
	case !webRoot(s.BaseURL):
		return "", "", invalid("基底網址必須是不含查詢字串的 http(s) URL，例如 https://gitlab.example.com")
	case s.APIURL != "" && !webRoot(s.APIURL):
		return "", "", invalid("API URL 必須是不含查詢字串的 http(s) URL")
	case len(token) > 1024:
		return "", "", invalid("Token 太長")
	case len(s.ClientID) > 256 || len(clientSecret) > 1024:
		return "", "", invalid("Client ID 或 Client Secret 太長")
	case s.OAuth() && token != "":
		return "", "", invalid("以 OAuth 授權的伺服器不能另外輸入存取 Token")
	case s.OAuth() && clientSecret == "" && s.oauth.ClientSecret == "":
		return "", "", invalid("請輸入 OAuth 應用程式的 Client Secret")
	}
	// The UI sends back the API URL it was shown: a default is no override.
	if s.APIURL == oldDefault || s.APIURL == defaultAPI(s.Kind, s.BaseURL) {
		s.APIURL = ""
	}
	return token, clientSecret, nil
}

// webRoot reports whether s is an absolute http(s) URL without credentials,
// query or fragment, not even an empty one.
func webRoot(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		u.User == nil && !strings.ContainsAny(s, "?# \t\r\n")
}

type failure struct {
	kind error
	msg  string
}

func (f failure) Error() string        { return f.msg }
func (f failure) Is(target error) bool { return target == f.kind }

func invalid(format string, args ...any) error {
	return failure{ErrInvalid, fmt.Sprintf(format, args...)}
}
