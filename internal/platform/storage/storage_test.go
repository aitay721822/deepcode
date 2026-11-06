package storage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFTSTriggersAndBackupRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "deepcode.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'abc')`,
		`INSERT INTO chunks(id, snapshot_id, path, symbol_name, start_line, end_line, content, content_hash)
		 VALUES (1, 1, 'src/auth.ts', 'login', 1, 9, 'export function login(user) {}', 'h1')`,
		`INSERT INTO chunks(id, snapshot_id, path, symbol_name, start_line, end_line, content, content_hash)
		 VALUES (2, 1, 'src/cart.ts', 'checkout', 1, 9, 'export function checkout() {}', 'h2')`,
		`UPDATE chunks SET content = 'export function signIn(user) {}' WHERE id = 1`,
		`DELETE FROM chunks WHERE id = 2`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	match := func(term string) int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH ?`, term).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if match("signin") != 1 || match("checkout") != 0 || match("auth") != 1 {
		t.Fatalf("FTS out of sync: signin=%d checkout=%d auth=%d", match("signin"), match("checkout"), match("auth"))
	}

	backup := filepath.Join(dir, "backup.db")
	if err := Backup(ctx, db, backup); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(backup); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup file mode %v, want 0600", fi.Mode().Perm())
	}
	if err := Backup(ctx, db, backup); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	db.Close()

	restored, err := Open(ctx, backup) // reopening also proves migrations are idempotent
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	restored.QueryRow(`SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH 'signin'`).Scan(&n)
	if n != 1 {
		t.Fatalf("restored database lost its index: %d", n)
	}
}

// TestOpenOddPath: a path holding # or % opens that very file. Written
// unescaped into the file: URI, # would start a fragment and % an escape.
func TestOpenOddPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "C# 100%25", "deepcode.db")
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// TestPorterMigrationRebuildsIndex: migration 002 replaces the full-text
// index and rebuilds it in the same transaction, so chunks indexed before it
// stay searchable.
func TestPorterMigrationRebuildsIndex(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "deepcode.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha) VALUES (1, 1, 'abc')`,
		`INSERT INTO chunks(id, snapshot_id, path, symbol_name, start_line, end_line, content, content_hash)
		 VALUES (1, 1, 'bus.go', 'Subscribe', 1, 9, 'func Subscribe(topic string) {}', 'h1')`,
		`DELETE FROM schema_migrations WHERE name LIKE '%002_fts_porter.sql'`, // so the next start runs it again
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()
	if db, err = Open(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	db.QueryRow(`SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH 'subscribers'`).Scan(&n)
	if n != 1 {
		t.Fatalf("after migration 002 the index finds %d chunks for a stemmed word, want 1", n)
	}
}
