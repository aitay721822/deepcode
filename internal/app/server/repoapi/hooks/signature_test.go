package hooks

import (
	"strings"
	"testing"
)

// TestVerify: GitHub's signature is the HMAC of the exact body and GitLab's
// token is the secret itself; anything else fails.
func TestVerify(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","after":"abc"}`)
	sig := GitHub("s3cret", body)
	if !strings.HasPrefix(sig, "sha256=") || !VerifyGitHub("s3cret", body, sig) {
		t.Fatalf("github signature %q", sig)
	}
	for _, bad := range []string{GitHub("other", body), "sha256=00", "md5=" + strings.TrimPrefix(sig, "sha256="), ""} {
		if VerifyGitHub("s3cret", body, bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	if !VerifyGitLab("s3cret", "s3cret") || VerifyGitLab("s3cret", "other") {
		t.Error("gitlab token")
	}
	secret, err := NewSecret()
	if err != nil || len(secret) != 64 {
		t.Fatalf("secret %q (%v)", secret, err)
	}
	if again, _ := NewSecret(); again == secret {
		t.Error("two secrets are equal")
	}
}
