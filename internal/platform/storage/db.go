// Package storage opens the SQLite database, applies migrations and takes backups.
package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"

	"modernc.org/sqlite" // registers the "sqlite" driver (pure Go, FTS5 included)

	"deepcode/internal/platform/storage/cuid"
)

// cuid() is a new CUID2 on every call (not deterministic, so each row of an
// UPDATE gets its own): a repository's public id, set by the trigger
// migration 010 adds. Only connections of this binary know the function, so
// another SQLite client cannot insert a repository.
func init() {
	sqlite.MustRegisterScalarFunction("cuid", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		return cuid.New(), nil
	})
}

// Open opens or creates the database at path and applies pending migrations.
// Write transactions start with BEGIN IMMEDIATE so they queue on busy_timeout
// instead of failing when a read transaction upgrades.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	// The path goes into a URI: escaped, its %, ? and # are not taken for
	// escapes, the query or a fragment.
	name := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	dsn := "file:" + name + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
