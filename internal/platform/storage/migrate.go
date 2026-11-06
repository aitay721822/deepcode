package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// The migrations: the first five in migrations/, later ones in a subfolder
// per area (migrations/knowledge/006_wiki_language.sql), since a folder holds
// at most five files. Their numbered file names set the order.
//
//go:embed migrations
var migrations embed.FS

// migrate applies the embedded migrations that have not run yet, in name order,
// each in its own transaction. Each is recorded as migrations/<file name>, as
// the first ones always were, so a database keeps matching a file that moves
// into a subfolder.
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	files, err := migrationFiles(migrations)
	if err != nil {
		return err
	}
	for _, file := range files {
		name := "migrations/" + path.Base(file)
		// A plain read first: an up-to-date database (backup next to a busy
		// server, say) never waits for the write lock.
		var done int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE name = ?`, name).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		if err := apply(ctx, db, name, file); err != nil {
			return err
		}
	}
	return nil
}

// migrationFiles lists the .sql files under migrations/ in fsys, in the order
// of their file names. Two files of one name would be recorded as one
// migration, so they are an error.
func migrationFiles(fsys fs.FS) ([]string, error) {
	var files []string
	seen := map[string]string{}
	err := fs.WalkDir(fsys, "migrations", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".sql" {
			return err
		}
		if other, ok := seen[path.Base(p)]; ok {
			return fmt.Errorf("migrations %s and %s share a file name", other, p)
		}
		seen[path.Base(p)] = p
		files = append(files, p)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return path.Base(files[i]) < path.Base(files[j]) })
	return files, err
}

// apply runs migration file, recorded as name, unless it ran before. The
// check shares the migration's BEGIN IMMEDIATE transaction, so two processes
// opening an old database (serve and backup, say) apply it once.
func apply(ctx context.Context, db *sql.DB, name, file string) error {
	body, err := migrations.ReadFile(file)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE name = ?`, name).Scan(&done); err != nil || done > 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(name) VALUES (?)`, name); err != nil {
		return err
	}
	return tx.Commit()
}
