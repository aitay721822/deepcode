// Package hooks receives a git server's push webhooks: a signed request that
// syncs the repository the hook belongs to.
package hooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// NewSecret is a random webhook secret, 32 bytes in hex.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GitHub is the X-Hub-Signature-256 of body under secret: sha256=<hex hmac>.
func GitHub(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyGitHub reports whether header is body's X-Hub-Signature-256 under
// secret.
func VerifyGitHub(secret string, body []byte, header string) bool {
	hexSum, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// VerifyGitLab reports whether token, X-Gitlab-Token, is secret, in constant
// time.
func VerifyGitLab(secret, token string) bool {
	return subtle.ConstantTimeCompare([]byte(secret), []byte(token)) == 1
}
