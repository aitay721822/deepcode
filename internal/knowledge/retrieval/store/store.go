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
	ChunkID   int64  `json:"-"` // a row id: never shown outside
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

// Read reads the chunks of ranked, in its order, up to limit hits, each hit on
// a file's header (the licence, package and imports before its first
// declaration) joined to the chunk declaring what follows it: alone the header
// shows no code, yet its imports can win a search for what the code does
// (OwnerRepository.java's JpaRepository), and the declaration may rank nowhere.
// When both are ranked they are read as one hit, in the place of the first,
// and a chunk joined is not read again; the places a join frees go to the
// ranked chunks after, so limit hits are read while ranked has them. Those
// past limit are read only as places fall free, as many as fell free. With
// perFile a file takes one place, its first hit's: a hit of a file already
// holding a place is left out and that place goes to the ranked chunks after,
// so one file's many chunks never crowd out the files ranked below them.
// ponytail: at search time, as joining them when chunking ranked the eval's
// Java corpora lower; chunk headers apart if a corpus needs it.
func (s *Store) Read(ctx context.Context, snapshotID int64, ranked []int64, limit int, perFile bool) ([]Hit, error) {
	var out []Hit
	read := map[int64]bool{} // chunks read, alone or joined
	take := func(h Hit) {
		if !perFile || !slices.ContainsFunc(out, func(o Hit) bool { return o.Path == h.Path }) {
			out = append(out, h)
		}
	}
	for next := 0; len(out) < limit && next < len(ranked); {
		batch := ranked[next:min(len(ranked), next+limit-len(out))]
		next += len(batch)
		hits, err := s.Chunks(ctx, batch)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			if read[h.ChunkID] {
				continue
			}
			read[h.ChunkID] = true
			if h.Symbol != "" || h.StartLine != 1 {
				take(h)
				continue
			}
			d, ok, err := s.Following(ctx, snapshotID, h.Path, h.EndLine+1)
			if err != nil {
				return nil, err
			}
			if !ok {
				take(h)
				continue
			}
			joined := Hit{ChunkID: h.ChunkID, Path: h.Path, Symbol: d.Symbol, StartLine: h.StartLine, EndLine: d.EndLine, Content: h.Content + "\n" + d.Content}
			if i := slices.IndexFunc(out, func(o Hit) bool { return o.ChunkID == d.ChunkID }); i >= 0 {
				out[i] = joined
				continue
			}
			read[d.ChunkID] = true
			take(joined)
		}
	}
	return out, nil
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

// Holding reads, for each (path, line) of the snapshot, the id of the chunk
// that holds the line: the file's first for line 0, 0 for a line no chunk
// holds, so that ids[i] is the i-th's.
func (s *Store) Holding(ctx context.Context, snapshotID int64, paths []string, lines []int) ([]int64, error) {
	ids := make([]int64, len(paths))
	for i, p := range paths {
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM chunks WHERE snapshot_id = ?1 AND path = ?2 AND end_line >= ?3 AND (start_line <= ?3 OR ?3 = 0)
			ORDER BY start_line LIMIT 1`, snapshotID, p, lines[i]).Scan(&ids[i])
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return ids, nil
}
