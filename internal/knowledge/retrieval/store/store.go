// Package store reads a snapshot's indexed chunks and symbols from SQLite for
// retrieval: the chunks that match a query's words or name its symbols, the
// files it names, and the chunks themselves.
package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"deepcode/internal/platform/storage"
)

// Store runs retrieval's SQL.
type Store struct{ DB *sql.DB }

type Hit struct {
	ChunkID   int64  `json:"chunkId"`
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Content   string `json:"content"`
}

// Readme returns the first two chunks of the snapshot's top-level README.
func (s *Store) Readme(ctx context.Context, snapshotID int64) ([]Hit, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+hitColumns+` FROM chunks
		WHERE snapshot_id = ? AND path NOT LIKE '%/%' AND lower(path) LIKE 'readme%' ORDER BY start_line LIMIT 2`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		h, err := scanHit(rows)
		if err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// hitColumns are the chunk columns scanHit reads.
const hitColumns = `id, path, symbol_name, start_line, end_line, content`

func scanHit(rows *sql.Rows) (Hit, error) {
	var h Hit
	err := rows.Scan(&h.ChunkID, &h.Path, &h.Symbol, &h.StartLine, &h.EndLine, &h.Content)
	return h, err
}

// Chunks reads the chunks for ids, keeping the order of ids.
func (s *Store) Chunks(ctx context.Context, ids []int64) ([]Hit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+hitColumns+` FROM chunks WHERE id IN (`+storage.Placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[int64]Hit{}
	for rows.Next() {
		h, err := scanHit(rows)
		if err != nil {
			return nil, err
		}
		byID[h.ChunkID] = h
	}
	hits := make([]Hit, 0, len(ids))
	for _, id := range ids {
		if h, ok := byID[id]; ok {
			hits = append(hits, h)
		}
	}
	return hits, rows.Err()
}

// Following reads the chunk of the snapshot's file p that starts at line and
// declares something, and whether there is one.
func (s *Store) Following(ctx context.Context, snapshotID int64, p string, line int) (Hit, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+hitColumns+` FROM chunks
		WHERE snapshot_id = ? AND path = ? AND start_line = ? AND symbol_name <> '' LIMIT 1`, snapshotID, p, line)
	if err != nil {
		return Hit{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Hit{}, false, rows.Err()
	}
	h, err := scanHit(rows)
	return h, err == nil, err
}

// Files are the paths of the snapshot's indexed files that names name, each
// name's in the order of names (none for a name naming none), at most limit
// in all: a path names its file (src/app.ts) or the files under it
// (examples/, src/app), a glob those matching it (tests/*.rs), a file name
// alone (App.java) the one file of that name, at the root or else the only
// one, as every project's appsettings.json is no one file. A path naming
// nothing, a leading folder left out of it (jmeter/plan.jmx), names what the
// one file or folder it ends with names. A name naming more files than are
// left of limit names none, as too wide to say what a query is about, and a
// file an earlier name named is that name's alone.
func (s *Store) Files(ctx context.Context, snapshotID int64, names []string, limit int) ([][]string, error) {
	out := make([][]string, len(names))
	n, seen := 0, map[string]bool{}
	// The exact path first, so that past limit files of a name the root's is kept.
	find := func(exact, glob string) ([]string, error) {
		return scanPaths(s.DB.QueryContext(ctx, `SELECT DISTINCT path FROM chunks
			WHERE snapshot_id = ? AND (path = ? OR path GLOB ?) ORDER BY path = ? DESC, path LIMIT ?`, snapshotID, exact, glob, exact, limit+1))
	}
	for i, name := range names {
		exact, glob, bare := name, "*/"+escapeGlob(name), false
		if strings.ContainsAny(name, "*?") {
			exact, glob = "", name
		} else if strings.Contains(name, "/") {
			exact = strings.TrimSuffix(name, "/")
			glob = escapeGlob(exact) + "/*"
		} else {
			bare = true
		}
		paths, err := find(exact, glob)
		if err == nil && len(paths) == 0 && !bare && exact != "" {
			paths, err = ending(find, exact)
		}
		if err != nil {
			return nil, err
		}
		if bare && slices.Contains(paths, name) {
			paths = []string{name}
		} else if bare && len(paths) > 1 {
			continue
		}
		paths = slices.DeleteFunc(paths, func(p string) bool { return seen[p] })
		if n+len(paths) > limit {
			continue
		}
		for _, p := range paths {
			seen[p] = true
		}
		out[i], n = paths, n+len(paths)
	}
	return out, nil
}

// First reads the ids of the first chunk of each of the snapshot's files in
// paths, in the order of paths.
func (s *Store) First(ctx context.Context, snapshotID int64, paths []string) ([]int64, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := []any{snapshotID}
	for _, p := range paths {
		args = append(args, p)
	}
	// A bare column beside min() is read from the row holding the minimum.
	rows, err := s.DB.QueryContext(ctx, `SELECT path, id, min(start_line) FROM chunks
		WHERE snapshot_id = ? AND path IN (`+storage.Placeholders(len(paths))+`) GROUP BY path`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byPath := map[string]int64{}
	for rows.Next() {
		var p string
		var id int64
		var start int
		if err := rows.Scan(&p, &id, &start); err != nil {
			return nil, err
		}
		byPath[p] = id
	}
	var ids []int64
	for _, p := range paths {
		if id, ok := byPath[p]; ok {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// ending is what find finds of the one file or folder whose path ends with
// tail, after a folder, each path once: none when two do (two projects'
// Events/).
func ending(find func(exact, glob string) ([]string, error), tail string) ([]string, error) {
	file, err := find("", "*/"+escapeGlob(tail))
	if err != nil {
		return nil, err
	}
	under, err := find("", "*/"+escapeGlob(tail)+"/*")
	if err != nil {
		return nil, err
	}
	paths, roots := slices.Compact(slices.Sorted(slices.Values(slices.Concat(file, under)))), map[string]bool{} // x/Events/Events is both
	for _, p := range paths {
		if i := strings.LastIndex(p+"/", "/"+tail+"/"); i >= 0 {
			roots[p[:i]] = true
		}
	}
	if len(roots) != 1 {
		return nil, nil
	}
	return paths, nil
}

// escapeGlob writes p as a GLOB pattern matching p alone: a path may hold
// brackets (app/[slug]/page.tsx).
func escapeGlob(p string) string {
	return strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]").Replace(p)
}

func scanPaths(rows *sql.Rows, err error) ([]string, error) {
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

func scanIDs(rows *sql.Rows, err error) ([]int64, error) {
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

// Holding reads the id of the chunk of each (path, line) of the snapshot
// that holds the line, in order: the file's first for line 0, none for a
// line no chunk holds.
func (s *Store) Holding(ctx context.Context, snapshotID int64, paths []string, lines []int) ([]int64, error) {
	var ids []int64
	for i, p := range paths {
		var id int64
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM chunks WHERE snapshot_id = ?1 AND path = ?2 AND end_line >= ?3 AND (start_line <= ?3 OR ?3 = 0)
			ORDER BY start_line LIMIT 1`, snapshotID, p, lines[i]).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
