package storage

import (
	"context"
	"database/sql"
	"os"
)

// Backup writes a transactionally consistent copy of src to dst without
// migrating src. VACUUM INTO fills an empty file, created first to guard
// against overwriting and readable only by its owner.
func Backup(ctx context.Context, src, dst string) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	db, err := sql.Open("sqlite", sqlitePath(src)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err == nil {
		defer db.Close()
		_, err = db.ExecContext(ctx, `VACUUM INTO ?`, dst)
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}
