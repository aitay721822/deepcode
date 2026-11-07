// Package secret seals the git servers' access tokens and OAuth secrets at
// rest, so the database alone does not give them away.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrSealed is a stored token this secret key cannot decrypt.
var ErrSealed = errors.New("無法用目前的加密金鑰解密 Token（DEEPCODE_SECRET_KEY 或 data/secret.key 是否已變更？）；請重新輸入 Token")

// Box seals access tokens with AES-256-GCM, so the database alone does not
// give them away.
type Box struct{ aead cipher.AEAD }

// OpenBox returns the box that seals tokens under secret or, when secret is
// "", under the random key in dir/secret.key, which it creates on the first
// start. Changing either leaves the stored tokens unreadable.
func OpenBox(dir, secret string) (*Box, error) {
	if secret != "" {
		key := sha256.Sum256([]byte(secret))
		return newBox(key[:])
	}
	path := filepath.Join(dir, "secret.key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key) // cannot fail since Go 1.24
		err = os.WriteFile(path, key, 0o600)
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must hold 32 bytes", path)
	}
	return newBox(key)
}

// newBox uses a 32-byte key.
func newBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead}, nil
}

// Seal returns base64(nonce | ciphertext); "" stays "".
func (b *Box) Seal(plain string) string {
	if plain == "" {
		return ""
	}
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil))
}

// Open reverses Seal.
func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("malformed sealed token")
	}
	plain, err := b.aead.Open(nil, raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():], nil)
	if err != nil {
		return "", ErrSealed
	}
	return string(plain), nil
}
