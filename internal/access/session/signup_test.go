package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"deepcode/internal/access/account"
	"deepcode/internal/access/role"
)

// TestSignUp: closed until opened; a sign-up gets the member role and a
// working session; per address, an invalid sign-up is given back while a
// taken name counts, up to the limit.
func TestSignUp(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	s.signups = limiter{max: 2, window: time.Minute}
	if _, _, err := s.SignUp(ctx, "ann", "", "password-1", "10.0.0.1"); !errors.Is(err, ErrSignUpClosed) {
		t.Fatalf("closed: %v", err)
	}
	if err := s.SetSignUpOpen(ctx, true); err != nil {
		t.Fatal(err)
	}
	token, p, err := s.SignUp(ctx, "ann", "Ann", "password-1", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Accounts.Get(ctx, p.UserID)
	if err != nil || p.DisplayName != "Ann" || len(u.Roles) != 1 || u.Roles[0].ID != role.Member {
		t.Fatalf("signed up as %+v with roles %+v (%v)", p, u.Roles, err)
	}
	if again, err := s.Authenticate(ctx, token); err != nil || again.UserID != p.UserID {
		t.Fatalf("session: %v", err)
	}

	// One sign-up counted so far, of two.
	if _, _, err := s.SignUp(ctx, "bob", "", "short", "10.0.0.1"); !errors.Is(err, account.ErrInvalid) {
		t.Fatalf("short password: %v", err)
	}
	if _, _, err := s.SignUp(ctx, "ANN", "", "password-2", "10.0.0.1"); !errors.Is(err, account.ErrConflict) {
		t.Fatalf("taken name: %v", err)
	}
	if _, _, err := s.SignUp(ctx, "cid", "", "password-3", "10.0.0.1"); !errors.Is(err, ErrSignUpLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, _, err := s.SignUp(ctx, "cid", "", "password-3", "10.0.0.2"); err != nil {
		t.Fatalf("another address: %v", err)
	}

	if err := s.SetSignUpOpen(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SignUp(ctx, "dee", "", "password-4", "10.0.0.3"); !errors.Is(err, ErrSignUpClosed) {
		t.Fatalf("closed again: %v", err)
	}
}
