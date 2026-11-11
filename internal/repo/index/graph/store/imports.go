// Package store keeps the import graph in SQLite: the modules each file
// content imports, read once, and each snapshot's edges between its files.
package store

import (
	"context"
	"database/sql"

	"deepcode/internal/platform/storage"
)

// Store runs the import graph's SQL.
type Store struct{ DB *sql.DB }

// grammars is the SQL condition that a source file's language is one of
// langs, with its arguments.
func grammars(langs []string) (string, []any) {
	args := make([]any, len(langs))
	for i, l := range langs {
		args[i] = l
	}
	return "f.language IN (" + storage.Placeholders(len(langs)) + ")", args
}

// Stale returns the ready snapshots that have files in langs but no edges.
func (s *Store) Stale(ctx context.Context, langs []string) ([]int64, error) {
	cond, args := grammars(langs)
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id FROM snapshots s WHERE s.status = 'ready'
		AND EXISTS (SELECT 1 FROM source_files f WHERE f.snapshot_id = s.id AND `+cond+`)
		AND NOT EXISTS (SELECT 1 FROM file_edges e WHERE e.snapshot_id = s.id) ORDER BY s.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Unread passes read the hash, a language and the content of up to limit of
// the contents of the snapshot's hand-written files in langs whose imports
// were never read, those past hash after, in hash order.
func (s *Store) Unread(ctx context.Context, snap int64, langs []string, after string, limit int, read func(hash, lang, content string)) error {
	cond, args := grammars(langs)
	rows, err := s.DB.QueryContext(ctx, `SELECT c.hash, min(f.language), c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND c.imports IS NULL AND c.hash > ? AND NOT f.generated AND `+cond+`
		GROUP BY c.hash ORDER BY c.hash LIMIT ?`, append(append([]any{snap, after}, args...), limit)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hash, lang, content string
		if err := rows.Scan(&hash, &lang, &content); err != nil {
			return err
		}
		read(hash, lang, content)
	}
	return rows.Err()
}

// Imports is what a content imports, as a file stores it.
type Imports struct{ Hash, Imports string }

// SetImports stores the imports of a batch of contents in one transaction.
func (s *Store) SetImports(ctx context.Context, batch []Imports) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, r := range batch {
		if _, err := tx.ExecContext(ctx, `UPDATE file_contents SET imports = ? WHERE hash = ?`, r.Imports, r.Hash); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// configs is the SQL condition that a file is one whose content resolving
// imports and entries reads, at any depth and in any case: go.mod, .NET
// project files, Cargo.toml, package.json and Maven's and Gradle's builds.
const configs = `(f.language = 'go.mod' OR lower(f.path) GLOB '*.csproj' OR lower(f.path) GLOB '*.vbproj' OR lower(f.path) GLOB '*.fsproj'
	OR lower(f.path) GLOB '*directory.build.props' OR lower(f.path) = 'cargo.toml' OR lower(f.path) GLOB '*/cargo.toml'
	OR lower(f.path) IN ('package.json', 'pom.xml', 'build.gradle', 'build.gradle.kts')
	OR lower(f.path) GLOB '*/package.json' OR lower(f.path) GLOB '*/pom.xml' OR lower(f.path) GLOB '*/build.gradle' OR lower(f.path) GLOB '*/build.gradle.kts')`

// Mains returns the paths of the snapshot's hand-written files declaring a
// function or method named main or Main: where a program starts.
func (s *Store) Mains(ctx context.Context, snap int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT s.path FROM symbols s
		JOIN source_files f ON f.snapshot_id = s.snapshot_id AND f.path = s.path
		WHERE s.snapshot_id = ? AND s.name IN ('main', 'Main') AND s.kind IN ('function', 'method') AND NOT f.generated ORDER BY s.path`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPaths(rows)
}

// Paths returns the paths of the snapshot's hand-written files, in any
// language: what a Markdown file may include.
func (s *Store) Paths(ctx context.Context, snap int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path FROM source_files WHERE snapshot_id = ? AND NOT generated ORDER BY path`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPaths(rows)
}

// Files passes file the path, language and stored imports of each of the
// snapshot's hand-written files in langs.
func (s *Store) Files(ctx context.Context, snap int64, langs []string, file func(path, lang, imports string)) error {
	cond, args := grammars(langs)
	rows, err := s.DB.QueryContext(ctx, `SELECT f.path, f.language, coalesce(c.imports, '') FROM source_files f
		JOIN file_contents c ON c.hash = f.content_hash WHERE f.snapshot_id = ? AND NOT f.generated AND `+cond, append([]any{snap}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path, lang, imports string
		if err := rows.Scan(&path, &lang, &imports); err != nil {
			return err
		}
		file(path, lang, imports)
	}
	return rows.Err()
}

// Configs returns the content of the snapshot's files that resolving imports
// reads, by path.
func (s *Store) Configs(ctx context.Context, snap int64) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT f.path, c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND `+configs, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contents := map[string]string{}
	for rows.Next() {
		var p, content string
		if err := rows.Scan(&p, &content); err != nil {
			return nil, err
		}
		contents[p] = content
	}
	return contents, rows.Err()
}
