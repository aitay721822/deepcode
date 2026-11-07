package graph

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"deepcode/internal/platform/storage"
)

// Dependencies returns up to limit files that paths import, paths themselves
// aside, those more of them import first. Only this direction: a file that
// many import, such as a package's context type, has every caller for an
// importer, and its callers say less of how it works than what it imports.
// ponytail: a barrel file (index.ts re-exporting a folder) brings in all it
// re-exports; rank by the hits' own imports if that crowds retrieval.
func Dependencies(ctx context.Context, db *sql.DB, snap int64, paths []string, limit int) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	in := storage.Placeholders(len(paths))
	args := []any{snap}
	for range 2 {
		for _, p := range paths {
			args = append(args, p)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT dst FROM file_edges WHERE snapshot_id = ? AND src IN (`+in+`) AND dst NOT IN (`+in+`)
		GROUP BY dst ORDER BY count(*) DESC, dst LIMIT ?`, append(args, limit)...)
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

// Impact returns the files beyond changed that a change from snapshot base to
// snap may make read differently: those that import, in either snapshot, a
// changed file whose declarations differ between the two (one lacking the
// file included). A change inside function bodies reaches no importer.
// ponytail: a declaration is compared by its first line, so a struct's new
// field or a Go interface's new method goes unseen; compare the chunks of
// declarations if that bites.
func Impact(ctx context.Context, db *sql.DB, base, snap int64, changed map[string]bool) (map[string]bool, error) {
	before, err := declarations(ctx, db, base, changed)
	if err != nil {
		return nil, err
	}
	after, err := declarations(ctx, db, snap, changed)
	if err != nil {
		return nil, err
	}
	moved := map[string]bool{}
	for p := range changed {
		if before[p] != after[p] {
			moved[p] = true
		}
	}
	out := map[string]bool{}
	if len(moved) == 0 {
		return out, nil
	}
	for _, s := range []int64{base, snap} {
		edges, err := loadEdges(ctx, db, s)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if moved[e.dst] && !changed[e.src] {
				out[e.src] = true
			}
		}
	}
	return out, nil
}

// declarations reads the declarations of the snapshot's files in paths, each
// file's as one string, in an order that moving them within the file keeps.
func declarations(ctx context.Context, db *sql.DB, snap int64, paths map[string]bool) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT path, kind || ' ' || qualified_name || ' ' || signature AS d FROM symbols
		WHERE snapshot_id = ? ORDER BY path, d`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := map[string][]string{}
	for rows.Next() {
		var p, d string
		if err := rows.Scan(&p, &d); err != nil {
			return nil, err
		}
		if paths[p] {
			parts[p] = append(parts[p], d)
		}
	}
	out := make(map[string]string, len(parts))
	for p, ds := range parts {
		out[p] = strings.Join(ds, "\n")
	}
	return out, rows.Err()
}

// DirEdge counts the imports from the files of one directory into another's.
type DirEdge struct {
	From, To string
	Count    int
}

// DirEdges returns the imports between the snapshot's directories, each cut
// to its first depth segments ("." for the root), most first; tests,
// examples and docs left out.
func DirEdges(ctx context.Context, db *sql.DB, snap int64, depth int) ([]DirEdge, error) {
	edges, err := loadEdges(ctx, db, snap)
	return group(edges, depth), err
}

// NewDirs returns the directories, cut to depth segments, that snapshot snap
// holds and base did not and that files outside them import, tests, examples
// and docs aside: a subsystem a change added and wired in.
func NewDirs(ctx context.Context, db *sql.DB, base, snap int64, depth int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT path FROM source_files WHERE snapshot_id = ?`, base)
	if err != nil {
		return nil, err
	}
	old := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		old[cut(p, depth)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	edges, err := loadEdges(ctx, db, snap)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		if d := cut(e.dst, depth); d != "." && !old[d] && !seen[d] && cut(e.src, depth) != d && !secondary(e.src) && !secondary(e.dst) {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

// loadEdges reads all of a snapshot's edges.
func loadEdges(ctx context.Context, db *sql.DB, snap int64) ([]edge, error) {
	rows, err := db.QueryContext(ctx, `SELECT src, dst FROM file_edges WHERE snapshot_id = ?`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []edge
	for rows.Next() {
		var e edge
		if err := rows.Scan(&e.src, &e.dst); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// group counts edges between directories cut to depth segments, most first,
// leaving out those within one and those of tests, examples and docs.
func group(edges []edge, depth int) []DirEdge {
	counts := map[[2]string]int{}
	for _, e := range edges {
		if from, to := cut(e.src, depth), cut(e.dst, depth); from != to && !secondary(e.src) && !secondary(e.dst) {
			counts[[2]string{from, to}]++
		}
	}
	out := make([]DirEdge, 0, len(counts))
	for k, n := range counts {
		out = append(out, DirEdge{From: k[0], To: k[1], Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.From < b.From || a.From == b.From && a.To < b.To
	})
	return out
}

// secondary reports a test, example or docs file, which retrieval ranks lower
// too (retrieval's secondary): what such a file imports shows how the code is
// used, not how its parts depend on each other, and examples alone can crowd
// a diagram of them.
func secondary(p string) bool {
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		switch d {
		case "test", "tests", "__tests__", "spec", "docs", "examples", "_examples":
			return true
		}
	}
	base := parts[len(parts)-1]
	return strings.HasPrefix(base, "test_") || strings.Contains(base, "_test.") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

// cut is the directory of file p cut to its first depth segments, "." at the
// root.
func cut(p string, depth int) string {
	d := dir(p)
	if d == "" {
		return "."
	}
	parts := strings.Split(d, "/")
	return strings.Join(parts[:min(depth, len(parts))], "/")
}
