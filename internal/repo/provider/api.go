package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"deepcode/internal/repo/gitserver"
)

// client follows redirects only within a host and scheme: GitLab's
// PRIVATE-TOKEN header is not one Go drops when a redirect leaves the host,
// and none may go from https to plain http.
var client = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		return http.ErrUseLastResponse
	}
	return nil
}}

// get calls endpoint on s's API with its token and decodes the JSON answer
// into v, classifying failures as ErrNotFound or ErrUpstream.
func get(ctx context.Context, s gitserver.Server, endpoint string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.APIURL+endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "deepcode")
	if s.Kind == gitserver.GitLab {
		if s.Token() != "" {
			req.Header.Set("PRIVATE-TOKEN", s.Token())
		}
	} else {
		req.Header.Set("Accept", "application/vnd.github+json")
		if s.Token() != "" {
			req.Header.Set("Authorization", "Bearer "+s.Token())
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("provider: %s: %v", s.Name, err) // may name internal addresses
		return failure{ErrUpstream, s.Name + " API is unreachable; an administrator can find the details in the server log"}
	}
	defer resp.Body.Close()
	switch code := resp.StatusCode; {
	case code == http.StatusOK:
	case code == http.StatusNotFound:
		return failure{ErrNotFound, "repository not found on " + s.Name + ", or its token cannot read it"}
	case code == http.StatusUnauthorized:
		return failure{ErrUpstream, s.Name + " rejected the access token"}
	case code == http.StatusTooManyRequests || code == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return failure{ErrUpstream, s.Name + " API rate limit reached; try again later, or give the server a token"}
	case code == http.StatusForbidden:
		return failure{ErrUpstream, s.Name + " refused the request (403): the token may lack a scope"}
	default:
		return failure{ErrUpstream, fmt.Sprintf("%s API: %s", s.Name, resp.Status)}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v); err != nil {
		return failure{ErrUpstream, fmt.Sprintf("%s API: unexpected answer: %v", s.Name, err)}
	}
	return nil
}

func githubMeta(ctx context.Context, s gitserver.Server, path string) (Meta, error) {
	var body struct {
		FullName      string  `json:"full_name"`
		Description   *string `json:"description"`
		DefaultBranch string  `json:"default_branch"`
		Stars         int     `json:"stargazers_count"`
		Size          int64   `json:"size"` // KB
	}
	if err := get(ctx, s, "/repos/"+path, &body); err != nil {
		return Meta{}, err
	}
	m := Meta{Path: path, Description: description(body.Description), DefaultBranch: body.DefaultBranch, Stars: body.Stars, SizeKB: body.Size}
	if strings.Count(body.FullName, "/") == 1 {
		m.Path = body.FullName // GitHub's canonical casing
	}
	return m, nil
}

func gitlabMeta(ctx context.Context, s gitserver.Server, path string) (Meta, error) {
	var body struct {
		PathWithNamespace string  `json:"path_with_namespace"`
		Description       *string `json:"description"`
		DefaultBranch     *string `json:"default_branch"`
		Stars             int     `json:"star_count"`
		Statistics        *struct {
			RepositorySize int64 `json:"repository_size"` // bytes; only for members
		} `json:"statistics"`
	}
	if err := get(ctx, s, "/projects/"+url.PathEscape(path)+"?statistics=true", &body); err != nil {
		return Meta{}, err
	}
	m := Meta{Path: path, Description: description(body.Description), Stars: body.Stars}
	if strings.Contains(body.PathWithNamespace, "/") {
		m.Path = body.PathWithNamespace
	}
	if body.DefaultBranch != nil {
		m.DefaultBranch = *body.DefaultBranch
	}
	if body.Statistics != nil {
		m.SizeKB = body.Statistics.RepositorySize >> 10
	}
	return m, nil
}

// maxDescription bounds a reported description, in characters. It is stored
// with the repository and shown on its card; one the size of a page comes from
// a broken or hostile server.
const maxDescription = 1000

// description is the description a server reported, "" for none, clipped.
func description(d *string) string {
	if d == nil {
		return ""
	}
	if r := []rune(*d); len(r) > maxDescription {
		return string(r[:maxDescription])
	}
	return *d
}
