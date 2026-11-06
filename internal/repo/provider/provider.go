// Package provider talks to the REST APIs of GitHub and GitLab, hosted or
// self-managed: repository details on import and connection checks.
package provider

import (
	"context"
	"errors"
	"fmt"

	"deepcode/internal/repo/gitserver"
)

// Meta is what an import needs to know about a repository.
type Meta struct {
	Path          string // canonical owner/name or group/.../name
	Description   string
	DefaultBranch string
	Stars         int
	SizeKB        int64 // 0 when the server does not say
}

// Kinds of failure a caller can act on.
var (
	ErrNotFound = errors.New("repository not found")
	ErrUpstream = errors.New("git server API failure") // unreachable, rate limited, token rejected
)

type failure struct {
	kind error
	msg  string
}

func (f failure) Error() string        { return f.msg }
func (f failure) Is(target error) bool { return target == f.kind }

// Fetch returns the details of the repository at path on s.
func Fetch(ctx context.Context, s gitserver.Server, path string) (Meta, error) {
	if s.Kind == gitserver.GitLab {
		return gitlabMeta(ctx, s, path)
	}
	return githubMeta(ctx, s, path)
}

// Check calls s's API with its token and says what that token can do.
func Check(ctx context.Context, s gitserver.Server) (string, error) {
	if s.Token() == "" {
		if s.Kind == gitserver.GitLab {
			var projects []struct{}
			if err := get(ctx, s, "/projects?per_page=1", &projects); err != nil {
				return "", err
			}
			return "Reachable without a token: only public projects can be imported.", nil
		}
		var limit struct {
			Rate struct{ Limit, Remaining int } `json:"rate"`
		}
		if err := get(ctx, s, "/rate_limit", &limit); err != nil {
			return "", err
		}
		return fmt.Sprintf("Reachable without a token: only public repositories can be imported (%d of %d API requests left this hour).",
			limit.Rate.Remaining, limit.Rate.Limit), nil
	}
	var user struct {
		Login    string `json:"login"`    // GitHub
		Username string `json:"username"` // GitLab
	}
	if err := get(ctx, s, "/user", &user); err != nil {
		return "", err
	}
	return "Signed in as " + user.Login + user.Username + ".", nil
}
