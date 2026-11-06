package storage

import (
	"context"
	"database/sql"
	"os"
)

// Backup writes a transactionally consistent copy of the live database to dst
// with VACUUM INTO, refusing to overwrite an existing file. The file is
// created first, readable by its owner only, since it holds password hashes,
// sessions and sealed tokens; VACUUM INTO fills an empty file.
func Backup(ctx context.Context, db *sql.DB, dst string) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}
