package gitserver

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

// segmentRe is one part of a repository path; a leading "-" could pass for a
// git option, and dots alone could climb directories.
var segmentRe = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]{0,254}$`)

// Resolve finds the server and repository path that raw names: the URL of a
// repository on one of the servers, or a bare path (owner/name, or
// group/subgroup/name on GitLab) on server id, the first server when id is 0.
// The server comes with its token unsealed.
func (sv *Service) Resolve(ctx context.Context, raw string, id int64) (Server, string, error) {
	servers, err := sv.List(ctx)
	if err != nil {
		return Server{}, "", err
	}
	s, path, err := match(servers, raw, id)
	if err != nil {
		return Server{}, "", err
	}
	s, err = sv.unseal(ctx, s)
	return s, path, err
}

func match(servers []Server, raw string, id int64) (Server, string, error) {
	raw = strings.TrimSpace(raw)
	var srv *Server
	rest := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Server{}, "", invalid("請輸入不含帳號密碼或查詢字串的 Repository URL")
		}
		for i := range servers { // the longest base wins: https://h/gitlab over https://h
			if p, ok := under(u, servers[i].BaseURL); ok && (srv == nil || len(servers[i].BaseURL) > len(srv.BaseURL)) {
				srv, rest = &servers[i], p
			}
		}
		if srv == nil {
			return Server{}, "", invalid("尚未設定 %s://%s 的 Git 伺服器；管理員可以新增", u.Scheme, u.Host)
		}
	} else {
		for i := range servers {
			if servers[i].ID == id || id == 0 && i == 0 {
				srv = &servers[i]
				break
			}
		}
		if srv == nil {
			return Server{}, "", invalid("Git 伺服器 %d 不存在", id)
		}
	}
	path, ok := clean(srv.Kind, rest)
	if !ok {
		if srv.Kind == GitHub {
			return Server{}, "", invalid("請以 owner/name 或 https://github.com/owner/name 的格式輸入 GitHub Repository")
		}
		return Server{}, "", invalid("請以 group/name（可含子群組）或其 URL 輸入 GitLab 專案")
	}
	return *srv, path, nil
}

// under reports whether u lies under the base URL and returns the rest of its path.
func under(u *url.URL, base string) (string, bool) {
	b, err := url.Parse(base)
	if err != nil || !strings.EqualFold(u.Scheme, b.Scheme) || !strings.EqualFold(u.Host, b.Host) {
		return "", false
	}
	prefix := strings.TrimSuffix(b.Path, "/") + "/"
	if strings.EqualFold(u.Path+"/", prefix) {
		return "", true // the base itself: clean then asks for a repository, rather than "no git server"
	}
	if len(u.Path) < len(prefix) || !strings.EqualFold(u.Path[:len(prefix)], prefix) {
		return "", false
	}
	return u.Path[len(prefix):], true
}

// ValidPath reports whether path is a repository path that clean leaves as
// it is: the check for a path a server names rather than a user.
func ValidPath(kind, path string) bool {
	p, ok := clean(kind, path)
	return ok && p == path
}

// clean turns the rest of a URL, or a bare path, into a repository path:
// GitHub's owner/name, ignoring pages below it such as /tree/main, or
// GitLab's group/.../name, ignoring pages below /-/.
func clean(kind, rest string) (string, bool) {
	rest = strings.Trim(rest, "/")
	if kind == GitLab {
		rest, _, _ = strings.Cut(rest, "/-/")
	}
	parts := strings.Split(rest, "/")
	if kind == GitHub && len(parts) > 2 {
		parts = parts[:2]
	}
	if len(parts) < 2 {
		return "", false
	}
	parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], ".git")
	for _, p := range parts {
		if !segmentRe.MatchString(p) || strings.Trim(p, ".") == "" {
			return "", false
		}
	}
	path := strings.Join(parts, "/")
	return path, len(path) <= 1024
}
