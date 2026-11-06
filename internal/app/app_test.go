package app

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"deepcode/internal/platform/storage"
)

// TestBadSettingCreatesNoAdmin: a setting that stops the start is caught
// before the first administrator is created, so no one-time password is
// printed for a server that never ran.
func TestBadSettingCreatesNoAdmin(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := New(ctx, Config{DataDir: dir, TrustedProxies: []string{"proxy.local"}}, db, http.NotFoundHandler(), true); err == nil {
		t.Fatal("started with a proxy that is no address")
	}
	var users int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("%d users after the failed start (%v)", users, err)
	}
}
