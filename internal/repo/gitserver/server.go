// Package gitserver keeps the git servers repositories are imported from:
// GitHub, GitLab, or a self-hosted instance of either, each with an optional
// access token sealed at rest, and knows their URLs: repository pages, clone
// URLs and links to lines.
package gitserver

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"deepcode/internal/repo/git"
)

const (
	GitHub = "github"
	GitLab = "gitlab"
)

// Kinds of failure a caller can act on; the HTTP layer maps each to a status.
var (
	ErrNotFound = errors.New("git server not found")
	ErrInvalid  = errors.New("invalid git server")
	ErrConflict = errors.New("git server conflict") // a duplicate, or in use by repositories
)

type Server struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	BaseURL         string `json:"baseUrl"`
	APIURL          string `json:"apiUrl"` // the override, else the kind's default
	HasToken        bool   `json:"hasToken"`
	RepositoryCount int    `json:"repositoryCount"`
	CreatedAt       string `json:"createdAt"`

	sealed string // the token as stored
	token  string // the token, once unsealed
}

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
	Name       *string `json:"name"`
	Kind       *string `json:"kind"`
	BaseURL    *string `json:"baseUrl"`
	APIURL     *string `json:"apiUrl"`
	Token      *string `json:"token"` // "" keeps the current token
	ClearToken bool    `json:"clearToken"`
}

// apply validates in and applies it to s; token is the new token, if any.
func (in Input) apply(s *Server) (token string, err error) {
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
	switch {
	case s.Name == "" || utf8.RuneCountInString(s.Name) > 64: // characters, not bytes
		return "", invalid("a name of 1 to 64 characters is required")
	case s.Kind != GitHub && s.Kind != GitLab:
		return "", invalid("kind must be github or gitlab")
	case !webRoot(s.BaseURL):
		return "", invalid("the base URL must be an http(s) URL such as https://gitlab.example.com, without a query")
	case s.APIURL != "" && !webRoot(s.APIURL):
		return "", invalid("the API URL must be an http(s) URL without a query")
	case len(token) > 1024:
		return "", invalid("the token is too long")
	}
	// The UI sends back the API URL it was shown: a default is no override.
	if s.APIURL == oldDefault || s.APIURL == defaultAPI(s.Kind, s.BaseURL) {
		s.APIURL = ""
	}
	return token, nil
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
