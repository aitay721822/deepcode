package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/url"
	"slices"
	"strings"
)

// redirectOK reports whether an app may register uri to receive codes:
// https, http only on a loopback host (a native app listening locally), or an
// app's own scheme; never a fragment. An app's scheme is a reversed domain
// (com.example.app:/cb, RFC 8252 section 7.1), or one of the editors' that
// came before that rule (cursor://anysphere.cursor-mcp/cb): any other
// (ms-msdt:, search-ms:, smb:, javascript:) would start whatever program, or
// page, claims it, with the code.
func redirectOK(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || len(uri) > 2000 || strings.Contains(uri, "#") || u.Scheme == "" {
		return false
	}
	switch scheme := strings.ToLower(u.Scheme); scheme {
	case "https":
		return u.Host != ""
	case "http":
		return loopback(u.Hostname())
	case "cursor", "vscode", "vscode-insiders":
		return true
	default:
		return strings.Contains(scheme, ".")
	}
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// registered reports whether uri is one of uris: exactly, or, for a loopback
// one, on another port, since a native app listens where it can (RFC 8252
// section 7.3).
func registered(uris []string, uri string) bool {
	if slices.Contains(uris, uri) {
		return true
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || !loopback(u.Hostname()) {
		return false
	}
	return slices.ContainsFunc(uris, func(r string) bool {
		v, err := url.Parse(r)
		return err == nil && v.Scheme == u.Scheme && v.Hostname() == u.Hostname() && v.Path == u.Path && v.RawQuery == u.RawQuery
	})
}

// back is uri with params added to its query, where an authorization
// response sends the browser (RFC 6749 section 4.1.2).
func back(uri string, params map[string]string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri // registered, so it parsed before
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// pkceOK reports whether verifier is the one whose S256 is challenge (RFC
// 7636): 43 to 128 characters, hashed and base64url-encoded without padding.
func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}
