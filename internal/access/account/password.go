package account

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"deepcode/internal/access/account/store"
)

// iterations follows OWASP's 2023 advice for PBKDF2-HMAC-SHA256.
const iterations = 600_000

// ErrBadLogin means the username or the password is wrong, or the user is disabled.
var ErrBadLogin = errors.New("使用者名稱或密碼錯誤")

// ErrWrongPassword is ChangePassword's answer to a wrong current password,
// a kind of ErrInvalid.
var ErrWrongPassword error = failure{ErrInvalid, "目前的密碼錯誤"}

// ErrStale is ChangePassword's answer when the user's password was changed,
// or the user disabled or deleted, while it ran: a kind of ErrConflict.
var ErrStale error = failure{ErrConflict, "你的帳號在這段期間已變更，新密碼未儲存；請重新登入"}

// hash returns a self-describing PBKDF2 hash: pbkdf2-sha256$iterations$salt$key.
func hash(password string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// verify reports whether password matches encoded, in constant time.
func verify(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	n, err := strconv.Atoi(parts[1])
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err != nil || err1 != nil || err2 != nil || n < 1 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, n, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// decoy is checked when the user does not exist, so the answer takes as long
// as for a wrong password and does not reveal which usernames exist.
var decoy, _ = hash("decoy")

// Check returns the id of the enabled user that username and password sign
// in, and the hash the password matched, which a new session is tied to.
func (s *Service) Check(ctx context.Context, username, password string) (int64, string, error) {
	id, encoded, disabled, err := s.Store.Credentials(ctx, strings.TrimSpace(username))
	if errors.Is(err, store.ErrNotFound) {
		verify(decoy, password)
		return 0, "", ErrBadLogin
	} else if err != nil {
		return 0, "", err
	}
	if !verify(encoded, password) || disabled {
		return 0, "", ErrBadLogin
	}
	return id, encoded, nil
}

// ChangePassword sets user id's password after checking the current one,
// signs the user out everywhere and returns the new hash. The new password is
// checked first, so refusing it costs no hashing.
func (s *Service) ChangePassword(ctx context.Context, id int64, current, next string) (string, error) {
	if err := checkPassword(next); err != nil {
		return "", err
	}
	encoded, err := s.Store.PasswordHash(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrStale // deleted since the request was signed in
	} else if err != nil {
		return "", err
	}
	if !verify(encoded, current) {
		return "", ErrWrongPassword
	}
	return s.replacePassword(ctx, id, encoded, next)
}

// replacePassword sets user id's password to next, already checked, only
// while old is still their hash and they are enabled, so a reset or a disable
// committed while the current password was being checked wins. Their sessions
// end in the same transaction.
func (s *Service) replacePassword(ctx context.Context, id int64, old, next string) (string, error) {
	h, err := hash(next)
	if err != nil {
		return "", err
	}
	err = s.Store.ReplacePassword(ctx, id, old, h)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrStale
	} else if err != nil {
		return "", err
	}
	return h, nil
}

func checkPassword(p string) error {
	if n := utf8.RuneCountInString(p); n < 8 || n > 256 { // characters, as people count them, not bytes
		return failure{ErrInvalid, "密碼長度必須為 8 到 256 個字元"}
	}
	return nil
}
