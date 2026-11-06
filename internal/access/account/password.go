package account

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// iterations follows OWASP's 2023 advice for PBKDF2-HMAC-SHA256.
const iterations = 600_000

// ErrBadLogin means the username or the password is wrong, or the user is disabled.
var ErrBadLogin = errors.New("invalid username or password")

// ErrWrongPassword is ChangePassword's answer to a wrong current password,
// a kind of ErrInvalid.
var ErrWrongPassword error = failure{ErrInvalid, "the current password is wrong"}

// ErrStale is ChangePassword's answer when the user's password was changed,
// or the user disabled or deleted, while it ran: a kind of ErrConflict.
var ErrStale error = failure{ErrConflict, "your account changed meanwhile and the new password was not saved; sign in again"}

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
func (s *Store) Check(ctx context.Context, username, password string) (int64, string, error) {
	var id int64
	var encoded string
	var disabled bool
	err := s.DB.QueryRowContext(ctx, `SELECT id, password_hash, disabled FROM users WHERE username = ?`, strings.TrimSpace(username)).
		Scan(&id, &encoded, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
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
func (s *Store) ChangePassword(ctx context.Context, id int64, current, next string) (string, error) {
	if err := checkPassword(next); err != nil {
		return "", err
	}
	var encoded string
	err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, id).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
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
func (s *Store) replacePassword(ctx context.Context, id int64, old, next string) (string, error) {
	h, err := hash(next)
	if err != nil {
		return "", err
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ? AND disabled = 0`, h, id, old)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrStale
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
		return err
	})
	if err != nil {
		return "", err
	}
	return h, nil
}

func checkPassword(p string) error {
	if n := utf8.RuneCountInString(p); n < 8 || n > 256 { // characters, as people count them, not bytes
		return failure{ErrInvalid, "a password of 8 to 256 characters is required"}
	}
	return nil
}
