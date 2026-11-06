package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deepcode/internal/access/account"
	"deepcode/internal/access/role"
	"deepcode/internal/platform/storage"
)

func setup(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []int64{1, 2} {
		db.Exec(`INSERT INTO repositories(id, url, owner, name) VALUES (?, ?, 'o', 'r')`, id, "https://github.com/o/r"+string(rune('0'+id)))
	}
	accounts := &account.Store{DB: db}
	if _, _, err := accounts.Bootstrap(context.Background(), "password-0"); err != nil { // an admin, as after any start
		t.Fatal(err)
	}
	return New(db, accounts, &role.Store{DB: db}, time.Hour), db
}

// user creates username with password-1 and roles.
func user(t *testing.T, s *Store, username string, roles ...int64) int64 {
	t.Helper()
	password := "password-1"
	u, err := s.Accounts.Create(context.Background(), account.Input{Username: &username, Password: &password, RoleIDs: &roles})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// TestLogin: sign-in ignores the username's case; failures are throttled per
// username and address; overlong usernames are not counted; signing out ends
// the session.
func TestLogin(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	user(t, s, "dev")
	token, p, err := s.Login(ctx, "DEV", "password-1", "1.1.1.1")
	if err != nil || p.Username != "dev" || token == "" {
		t.Fatalf("sign in: %+v %v", p, err)
	}
	for range 10 {
		if _, _, err := s.Login(ctx, "dev", "wrong", "3.3.3.3"); !errors.Is(err, account.ErrBadLogin) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	if _, _, err := s.Login(ctx, "dev", "password-1", "3.3.3.3"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("after ten failures: %v", err)
	}
	if _, _, err := s.Login(ctx, "dev", "password-1", "4.4.4.4"); err != nil {
		t.Fatalf("from another address: %v", err)
	}
	if _, _, err := s.Login(ctx, strings.Repeat("x", 100), "p", "5.5.5.5"); !errors.Is(err, account.ErrBadLogin) || len(s.byUser.counts) != 1 {
		t.Fatalf("an overlong username: %v, %d keys", err, len(s.byUser.counts))
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("signed out, still signed in: %v", err)
	}
}

// TestNoStaleSession: a user disabled, or given a new password, after their
// password was checked gets no session, so none outlives the change.
func TestNoStaleSession(t *testing.T) {
	s, db := setup(t)
	ctx := context.Background()
	id := user(t, s, "dev")
	_, checked, err := s.Accounts.Check(ctx, "dev", "password-1")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, id)
	if _, err := s.start(ctx, id, checked); !errors.Is(err, ErrNoSession) {
		t.Fatalf("a disabled user got a session: %v", err)
	}
	db.Exec(`UPDATE users SET disabled = 0 WHERE id = ?`, id)
	reset := "password-2"
	if _, err := s.Accounts.Update(ctx, id, 1, account.Input{Password: &reset}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.start(ctx, id, checked); !errors.Is(err, ErrNoSession) {
		t.Fatalf("a session outlived a password reset: %v", err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sessions stored", n)
	}
}

// TestLoginPerAddress: an address that fails too often, whatever usernames it
// tries, is refused; other addresses are not.
func TestLoginPerAddress(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	user(t, s, "dev")
	s.byAddress.max = 3 // fifty in production; PBKDF2 makes each failure slow
	for _, name := range []string{"a", "b", "c"} {
		if _, _, err := s.Login(ctx, name, "wrong", "6.6.6.6"); !errors.Is(err, account.ErrBadLogin) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, _, err := s.Login(ctx, "dev", "password-1", "6.6.6.6"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("after three failures from one address: %v", err)
	}
	if _, _, err := s.Login(ctx, "dev", "password-1", "7.7.7.7"); err != nil {
		t.Fatalf("from another address: %v", err)
	}
}

// TestAttemptsGivenBack: sign-ins that succeed, and password changes that
// fail for another reason than the current password, are not counted, so an
// office behind one address never locks itself out by signing in.
func TestAttemptsGivenBack(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	dev := user(t, s, "dev")
	s.byUser.max, s.byAddress.max = 2, 2 // ten and fifty in production; PBKDF2 makes each check slow
	for range 3 {
		if _, _, err := s.Login(ctx, "dev", "password-1", "9.9.9.9"); err != nil {
			t.Fatalf("sign-in: %v", err)
		}
		if _, err := s.ChangePassword(ctx, dev, "password-1", "short"); err == nil || errors.Is(err, account.ErrWrongPassword) || errors.Is(err, ErrThrottled) {
			t.Fatalf("a new password too short: %v", err)
		}
	}
	// Nor is a change that lost the race with a disable.
	if _, err := s.Accounts.Update(ctx, dev, 1, account.Input{Disabled: new(true)}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := s.ChangePassword(ctx, dev, "password-1", "password-2"); !errors.Is(err, account.ErrStale) {
			t.Fatalf("a change after a disable: %v", err)
		}
	}
}

// TestUndoAfterWindow: an attempt given back after its window ended (the
// window turned during the password check) leaves the next window's count be.
func TestUndoAfterWindow(t *testing.T) {
	l := limiter{max: 2, window: time.Minute}
	old := l.take("k")
	old.since = old.since.Add(-time.Minute) // its window ends while the check runs
	l.take("k")                             // an attempt in the next window
	l.undo("k", old)
	if c := l.counts["k"]; c == nil || c.n != 1 {
		t.Fatalf("the next window lost its attempt: %+v", c)
	}
}

// TestLoginBurst: parallel guesses are counted as they start, so no more of
// them try a password than the limit allows.
func TestLoginBurst(t *testing.T) {
	s, _ := setup(t)
	user(t, s, "dev")
	var tried atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() {
			if _, _, err := s.Login(context.Background(), "dev", "wrong", "8.8.8.8"); errors.Is(err, account.ErrBadLogin) {
				tried.Add(1)
			}
		})
	}
	wg.Wait()
	if n := tried.Load(); n != 10 {
		t.Fatalf("%d of 30 parallel guesses were tried, want 10", n)
	}
}

// TestGrantsFollowRoles: every request sees the user's roles as they are now.
func TestGrantsFollowRoles(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	readers, _ := s.Roles.Create(ctx, role.Input{Name: new("readers"), Permissions: &[]role.Permission{role.RepoRead}, RepositoryIDs: &[]int64{1}})
	user(t, s, "dev", readers.ID)
	token, p, err := s.Login(ctx, "dev", "password-1", "1.1.1.1")
	if err != nil || !p.CanOn(role.RepoRead, 1) || p.CanOn(role.RepoRead, 2) {
		t.Fatalf("dev: %+v %v", p, err)
	}
	if _, err := s.Roles.Update(ctx, readers.ID, role.Input{RepositoryIDs: &[]int64{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.Authenticate(ctx, token); !p.CanOn(role.RepoRead, 2) {
		t.Fatal("a repository added to the role stays hidden")
	}
}

// TestChangePassword: a new password ends the old sessions and starts one;
// wrong current passwords are throttled like failed sign-ins.
func TestChangePassword(t *testing.T) {
	s, db := setup(t)
	ctx := context.Background()
	dev := user(t, s, "dev")
	old, _, _ := s.Login(ctx, "dev", "password-1", "1.1.1.1")
	for range 10 {
		if _, err := s.ChangePassword(ctx, dev, "wrong", "password-2"); !errors.Is(err, account.ErrWrongPassword) {
			t.Fatalf("wrong current password: %v", err)
		}
	}
	if _, err := s.ChangePassword(ctx, dev, "password-1", "password-2"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("after ten wrong passwords: %v", err)
	}
	fresh := New(db, s.Accounts, s.Roles, time.Hour) // as after the 15 minutes
	token, err := fresh.ChangePassword(ctx, dev, "password-1", "password-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Authenticate(ctx, old); !errors.Is(err, ErrNoSession) {
		t.Fatalf("the old session survived: %v", err)
	}
	if p, err := fresh.Authenticate(ctx, token); err != nil || p.UserID != dev {
		t.Fatalf("the new session: %+v %v", p, err)
	}
}
