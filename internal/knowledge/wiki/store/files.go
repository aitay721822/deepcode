package store

import (
	"context"
	"encoding/json"
)

// FileCount counts the snapshot's indexed files, generated ones aside.
func (s *Store) FileCount(ctx context.Context, snap int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM source_files WHERE snapshot_id = ? AND NOT generated`, snap).Scan(&n)
	return n, err
}

// Paths returns the paths of the snapshot's files, generated ones aside, in
// order.
func (s *Store) Paths(ctx context.Context, snap int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path FROM source_files WHERE snapshot_id = ? AND NOT generated ORDER BY path`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Lines returns the line count of each of the snapshot's files, generated
// ones aside.
func (s *Store) Lines(ctx context.Context, snap int64) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path, line_count FROM source_files WHERE snapshot_id = ? AND NOT generated`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

// Declared calls each with the path and name of every type and function the
// snapshot's files declare, and every section of its guides, by path and
// then line.
func (s *Store) Declared(ctx context.Context, snap int64, each func(path, name string)) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT path, name FROM symbols WHERE snapshot_id = ?
		AND kind IN ('class', 'struct', 'interface', 'trait', 'enum', 'record', 'type', 'function', 'macro', 'union', 'section') ORDER BY path, start_line`, snap)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p, name string
		if err := rows.Scan(&p, &name); err != nil {
			return err
		}
		each(p, name)
	}
	return rows.Err()
}

// Symbols returns, for each of paths the snapshot holds chunks of, the
// symbols they are of ("" for code outside any declaration).
func (s *Store) Symbols(ctx context.Context, snap int64, paths []string) (map[string]map[string]bool, error) {
	list, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT path, coalesce(symbol_name, '') FROM chunks
		WHERE snapshot_id = ? AND path IN (SELECT value FROM json_each(?))`, snap, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var p, symbol string
		if err := rows.Scan(&p, &symbol); err != nil {
			return nil, err
		}
		if out[p] == nil {
			out[p] = map[string]bool{}
		}
		out[p][symbol] = true
	}
	return out, rows.Err()
}

// File is one file of a snapshot with its content.
type File struct{ Path, Content string }

// RootFiles returns the snapshot's top-level README and build manifests, the
// README first, a Markdown one before others and the shortest named so
// (README.md before README and README.zh-CN.md).
func (s *Store) RootFiles(ctx context.Context, snap int64) ([]File, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT f.path, c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND f.path NOT LIKE '%/%'
		  AND (lower(f.path) LIKE 'readme%' OR lower(f.path) IN ('package.json', 'go.mod', 'cargo.toml', 'pyproject.toml',
		    'pom.xml', 'build.gradle', 'build.gradle.kts', 'settings.gradle', 'settings.gradle.kts')
		    OR lower(f.path) LIKE '%.sln' OR lower(f.path) LIKE '%.slnx')
		ORDER BY lower(f.path) LIKE 'readme%' DESC, lower(f.path) LIKE '%.md' DESC, length(f.path), f.path`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.Content); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
