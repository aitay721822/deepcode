// Package graph links a snapshot's files by their imports, Go's through
// go.mod and JavaScript's and TypeScript's through relative paths, and
// answers what retrieval and the wiki ask of those links.
package graph

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/analyzer"
)

// How many files' imports Link reads, and how many edges it stores or
// deletes, in one transaction.
const (
	readBatch  = 200
	storeBatch = 5000
)

// Imports is how a file's imports are stored: a line each.
func Imports(specs []string) string { return strings.Join(specs, "\n") }

// grammars is the SQL condition that a source file's language has a grammar,
// with its arguments.
func grammars() (string, []any) {
	langs := analyzer.Grammars()
	args := make([]any, len(langs))
	for i, l := range langs {
		args[i] = l
	}
	return "f.language IN (" + storage.Placeholders(len(langs)) + ")", args
}

// Link reads the imports the snapshot's files lack, which an older version
// stored without, then rebuilds the snapshot's edges. It returns how many
// edges it stored; a pruned snapshot gets none.
func Link(ctx context.Context, db *sql.DB, snap int64) (int, error) {
	if err := readImports(ctx, db, snap); err != nil {
		return 0, err
	}
	files, configs, err := load(ctx, db, snap)
	if err != nil {
		return 0, err
	}
	return store(ctx, db, snap, resolve(files, configs))
}

// LinkStale links the ready snapshots that have files with a grammar but no
// edges: those published before this version, or by an older one run since.
// One whose files import none of each other is linked again at every start,
// which costs a read of its imports. It returns how many it linked.
func LinkStale(ctx context.Context, db *sql.DB) (int, error) {
	cond, args := grammars()
	rows, err := db.QueryContext(ctx, `SELECT s.id FROM snapshots s WHERE s.status = 'ready'
		AND EXISTS (SELECT 1 FROM source_files f WHERE f.snapshot_id = s.id AND `+cond+`)
		AND NOT EXISTS (SELECT 1 FROM file_edges e WHERE e.snapshot_id = s.id) ORDER BY s.id`, args...)
	ids, err := scanIDs(rows, err)
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		if _, err := Link(ctx, db, id); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

// readImports parses the files of the snapshot whose imports were never read,
// readBatch at a time, so no transaction holds the write lock for long.
func readImports(ctx context.Context, db *sql.DB, snap int64) error {
	p := analyzer.NewParser()
	defer p.Close()
	cond, langs := grammars()
	type read struct{ hash, imports string }
	for last := ""; ; {
		rows, err := db.QueryContext(ctx, `SELECT c.hash, min(f.language), c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
			WHERE f.snapshot_id = ? AND c.imports IS NULL AND c.hash > ? AND NOT f.generated AND `+cond+`
			GROUP BY c.hash ORDER BY c.hash LIMIT ?`, append(append([]any{snap, last}, langs...), readBatch)...)
		if err != nil {
			return err
		}
		var batch []read
		for rows.Next() {
			var hash, lang, content string
			if err := rows.Scan(&hash, &lang, &content); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, read{hash, Imports(p.Parse(lang, []byte(content)).Imports)})
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(batch) == 0 {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, r := range batch {
			if _, err := tx.ExecContext(ctx, `UPDATE file_contents SET imports = ? WHERE hash = ?`, r.imports, r.hash); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		last = batch[len(batch)-1].hash
	}
}

// configs is the SQL condition that a file is one whose content resolving
// imports reads: go.mod, .NET project files, Cargo.toml, in any case.
const configs = `(f.language = 'go.mod' OR lower(f.path) GLOB '*.csproj' OR lower(f.path) GLOB '*.vbproj' OR lower(f.path) GLOB '*.fsproj'
	OR lower(f.path) GLOB '*directory.build.props' OR lower(f.path) = 'cargo.toml' OR lower(f.path) GLOB '*/cargo.toml')`

// load reads the snapshot's files with a grammar, generated ones aside, with
// their imports, and the content of its configs by path.
func load(ctx context.Context, db *sql.DB, snap int64) ([]file, map[string]string, error) {
	cond, langs := grammars()
	rows, err := db.QueryContext(ctx, `SELECT f.path, f.language, coalesce(c.imports, '') FROM source_files f
		JOIN file_contents c ON c.hash = f.content_hash WHERE f.snapshot_id = ? AND NOT f.generated AND `+cond, append([]any{snap}, langs...)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var files []file
	for rows.Next() {
		var f file
		var imports string
		if err := rows.Scan(&f.path, &f.lang, &imports); err != nil {
			return nil, nil, err
		}
		if imports != "" {
			f.imports = strings.Split(imports, "\n")
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	crows, err := db.QueryContext(ctx, `SELECT f.path, c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND `+configs, snap)
	if err != nil {
		return nil, nil, err
	}
	defer crows.Close()
	contents := map[string]string{}
	for crows.Next() {
		var p, content string
		if err := crows.Scan(&p, &content); err != nil {
			return nil, nil, err
		}
		contents[p] = content
	}
	return files, contents, crows.Err()
}

// store replaces the snapshot's edges, storeBatch at a time so no
// transaction holds the write lock for long (a Go monorepo has millions),
// and stops once the snapshot is pruned or gone: prune would not come back
// for the rows. It returns how many it stored.
// ponytail: a crash between batches leaves part of the edges, which LinkStale
// takes for a linked snapshot; the repository's next index replaces them.
func store(ctx context.Context, db *sql.DB, snap int64, edges []edge) (int, error) {
	// A served snapshot that has edges, read again after an analyzer upgrade,
	// swaps them in one transaction, so its readers never see half a graph.
	// ponytail: that transaction holds the write lock for all of a monorepo's edges.
	var swap bool
	if err := db.QueryRowContext(ctx, `SELECT status = 'ready' AND EXISTS (SELECT 1 FROM file_edges WHERE snapshot_id = ?1)
		FROM snapshots WHERE id = ?1`, snap).Scan(&swap); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	if swap {
		return replace(ctx, db, snap, edges)
	}
	if err := Unlink(ctx, db, snap); err != nil {
		return 0, err
	}
	for i := 0; i < len(edges); i += storeBatch {
		if pruned, err := insert(ctx, db, snap, edges[i:min(i+storeBatch, len(edges))]); err != nil || pruned {
			return 0, err
		}
	}
	return len(edges), nil
}

// Unlink deletes the snapshot's edges, storeBatch at a time so no statement
// holds the write lock for long.
func Unlink(ctx context.Context, db *sql.DB, snap int64) error {
	for {
		res, err := db.ExecContext(ctx, `DELETE FROM file_edges WHERE snapshot_id = ?1 AND (src, dst) IN
			(SELECT src, dst FROM file_edges WHERE snapshot_id = ?1 LIMIT ?2)`, snap, storeBatch)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n < storeBatch {
			return err
		}
	}
}

// insert writes one batch of store's in a transaction, or reports a snapshot
// pruned or deleted (with its repository) meanwhile.
func insert(ctx context.Context, db *sql.DB, snap int64, batch []edge) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snap).Scan(&status); errors.Is(err, sql.ErrNoRows) || status == "pruned" {
		return true, nil
	} else if err != nil {
		return false, err
	}
	if err := insertEdges(ctx, tx, snap, batch); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// replace swaps the snapshot's edges for edges in one transaction, unless the
// snapshot was pruned or deleted meanwhile. It returns how many it stored.
func replace(ctx context.Context, db *sql.DB, snap int64, edges []edge) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snap).Scan(&status); errors.Is(err, sql.ErrNoRows) || status == "pruned" {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM file_edges WHERE snapshot_id = ?`, snap); err != nil {
		return 0, err
	}
	if err := insertEdges(ctx, tx, snap, edges); err != nil {
		return 0, err
	}
	return len(edges), tx.Commit()
}

// insertEdges adds edges to the snapshot; one there already (a link at start
// meeting an index job's) is kept.
func insertEdges(ctx context.Context, tx *sql.Tx, snap int64, edges []edge) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO file_edges(snapshot_id, src, dst) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range edges {
		if _, err := stmt.ExecContext(ctx, snap, e.src, e.dst); err != nil {
			return err
		}
	}
	return nil
}

// scanIDs reads the ids that a query of one integer column returned.
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
