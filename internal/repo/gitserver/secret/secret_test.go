package secret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenBox: the first start creates the key file and later starts use
// it; a damaged one stops the start; a secret overrides the file; another
// key cannot open what this one sealed.
func TestOpenBox(t *testing.T) {
	dir := t.TempDir()
	first, err := OpenBox(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenBox(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := second.Open(first.Seal("token")); err != nil || plain != "token" {
		t.Fatalf("the key changed between starts: %q %v", plain, err)
	}
	if first.Seal("token") == first.Seal("token") {
		t.Fatal("two seals of one token match: the nonce repeats")
	}
	a, _ := OpenBox(dir, "pass phrase")
	b, _ := OpenBox(t.TempDir(), "pass phrase")
	if plain, err := b.Open(a.Seal("token")); err != nil || plain != "token" {
		t.Fatalf("secret: %q %v", plain, err)
	}
	other, _ := newBox([]byte(strings.Repeat("k", 32)))
	if _, err := other.Open(a.Seal("x")); !errors.Is(err, ErrSealed) {
		t.Fatal("opened a token sealed under another key")
	}
	os.WriteFile(filepath.Join(dir, "secret.key"), []byte("short"), 0o600)
	if _, err := OpenBox(dir, ""); err == nil {
		t.Fatal("used a 5-byte key file")
	}
}
