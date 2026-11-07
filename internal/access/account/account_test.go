package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"deepcode/internal/access/account/store"
	"deepcode/internal/access/role"
	"deepcode/internal/platform/storage"
)

func setup(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Service{Store: &store.Store{DB: db}}, db
}

func str(s string) *string    { return &s }
func ids(v ...int64) *[]int64 { return &v }

// TestBootstrap: the first start creates admin, with a generated password,
// and later starts do nothing.
func TestBootstrap(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	created, password, err := st.Bootstrap(ctx, "")
	if err != nil || !created || len(password) < 12 {
		t.Fatalf("bootstrap: %v %q %v", created, password, err)
	}
	if again, _, _ := st.Bootstrap(ctx, ""); again {
		t.Fatal("bootstrapped twice")
	}
	id, _, err := st.Check(ctx, "ADMIN", password)
	first, _ := st.FirstAdmin(ctx)
	if err != nil || first != id {
		t.Fatalf("sign in %d, first admin %d: %v", id, first, err)
	}
	if u, err := st.ByUsername(ctx, " Admin "); err != nil || u.ID != id || len(u.Roles) != 1 || u.Roles[0].ID != role.Admin {
		t.Fatalf("by username: %+v %v", u, err)
	}
	if _, _, err := st.Check(ctx, "admin", "wrong-password"); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("wrong password: %v", err)
	}
}

// TestPasswordLength: 8 to 256 characters, counted as characters, not bytes.
func TestPasswordLength(t *testing.T) {
	for p, ok := range map[string]bool{"1234567": false, "12345678": true, "密碼密碼": false, "密碼密碼密碼密碼": true,
		strings.Repeat("密", 256): true, strings.Repeat("a", 257): false} {
		if err := checkPassword(p); (err == nil) != ok {
			t.Errorf("%q: %v", p, err)
		}
	}
}

// TestDisplayName: 1 to 64 characters, not bytes, with no control character,
// line break or bidi control; joiners are fine.
func TestDisplayName(t *testing.T) {
	for n, ok := range map[string]bool{"Ann Lee": true, "林\u3000中宇": true, strings.Repeat("林", 64): true, strings.Repeat("a", 65): false,
		"": false, "An\tn": false, "Ann\u2028Lee": false, "Ann\u202eLee": false, "Ann\u2067Lee": false,
		"Ann\u200cLee": true, "👩\u200d💻": true} {
		if err := checkDisplay(n); (err == nil) != ok {
			t.Errorf("%q: %v", n, err)
		}
	}
}

// TestKeepAdmin: an enabled admin always remains, and nobody removes or
// disables themselves.
func TestKeepAdmin(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	st.Bootstrap(ctx, "password-0")
	admin, _ := st.FirstAdmin(ctx)
	dev, err := st.Create(ctx, Input{Username: str("dev"), Password: str("password-1")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Update(ctx, admin, dev.ID, Input{RoleIDs: ids(2)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("demoted the last admin: %v", err)
	}
	if _, err := st.Update(ctx, admin, dev.ID, Input{Disabled: new(true)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled the last admin: %v", err)
	}
	if err := st.Delete(ctx, admin, dev.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted the last admin: %v", err)
	}
	if err := st.Delete(ctx, admin, admin); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted yourself: %v", err)
	}
	if _, err := st.Update(ctx, admin, admin, Input{Disabled: new(true)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled yourself: %v", err)
	}
	// With a second admin, the first may go.
	if _, err := st.Update(ctx, dev.ID, admin, Input{RoleIDs: ids(role.Admin)}); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, admin, dev.ID); err != nil {
		t.Fatalf("deleted one of two admins: %v", err)
	}
	if _, err := st.Create(ctx, Input{Username: str("DEV"), Password: str("password-2")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("the same username in other letter case: %v", err)
	}
	if _, err := st.Update(ctx, 99, dev.ID, Input{DisplayName: str("Ghost")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("updated a missing user: %v", err)
	}
}

// TestSignOutOnChange: a new password or disabling ends the user's sessions
// in the same transaction; other edits keep them.
func TestSignOutOnChange(t *testing.T) {
	st, db := setup(t)
	ctx := context.Background()
	st.Bootstrap(ctx, "password-0")
	admin, _ := st.FirstAdmin(ctx)
	dev, _ := st.Create(ctx, Input{Username: str("dev"), Password: str("password-1")})
	n := 0
	signIn := func() {
		n++
		db.Exec(`INSERT INTO sessions(token_hash, user_id, expires_at) VALUES (?, ?, datetime('now', '+1 hour'))`, fmt.Sprint("token-", n), dev.ID)
	}
	sessions := func() (n int) {
		db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, dev.ID).Scan(&n)
		return n
	}
	signIn()
	if u, err := st.Update(ctx, dev.ID, admin, Input{DisplayName: str("Dev One")}); err != nil || u.DisplayName != "Dev One" || u.Disabled || sessions() != 1 {
		t.Fatalf("rename: %+v %v, %d sessions", u, err, sessions())
	}
	if _, err := st.ChangePassword(ctx, dev.ID, "wrong-password", "password-2"); !errors.Is(err, ErrWrongPassword) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong current password: %v", err)
	}
	// The new password is checked first, before the current one is hashed.
	if _, err := st.ChangePassword(ctx, dev.ID, "wrong-password", "short"); errors.Is(err, ErrWrongPassword) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("short new password: %v", err)
	}
	if _, err := st.ChangePassword(ctx, dev.ID, "password-1", "password-2"); err != nil || sessions() != 0 {
		t.Fatalf("password change: %v, %d sessions", err, sessions())
	}
	// A reset committed while the current password was being checked wins.
	if _, err := st.replacePassword(ctx, dev.ID, "a hash replaced meanwhile", "password-3"); !errors.Is(err, ErrStale) || !errors.Is(err, ErrConflict) {
		t.Fatalf("replaced over a stale hash: %v", err)
	}
	if _, _, err := st.Check(ctx, "dev", "password-2"); err != nil {
		t.Fatalf("the password set before was lost: %v", err)
	}
	signIn()
	if _, err := st.Update(ctx, dev.ID, admin, Input{Disabled: new(true)}); err != nil || sessions() != 0 {
		t.Fatalf("disable: %v, %d sessions", err, sessions())
	}
	if _, _, err := st.Check(ctx, "dev", "password-2"); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("a disabled user signed in: %v", err)
	}
	// A disable wins too: no new password, even with the right current one.
	if _, err := st.ChangePassword(ctx, dev.ID, "password-2", "password-3"); !errors.Is(err, ErrStale) {
		t.Fatalf("a disabled user changed their password: %v", err)
	}
}
