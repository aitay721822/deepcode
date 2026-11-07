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
	files, modules, err := load(ctx, db, snap)
	if err != nil {
		return 0, err
	}
	return store(ctx, db, snap, resolve(files, modules))
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
			WHERE f.snapshot_id = ? AND c.imports IS NULL AND c.hash > ? AND `+cond+`
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

// load reads the snapshot's files with a grammar, their imports, and the
// module path of each of its go.mod files, by directory.
func load(ctx context.Context, db *sql.DB, snap int64) ([]file, map[string]string, error) {
	cond, langs := grammars()
	rows, err := db.QueryContext(ctx, `SELECT f.path, f.language, coalesce(c.imports, '') FROM source_files f
		JOIN file_contents c ON c.hash = f.content_hash WHERE f.snapshot_id = ? AND `+cond, append([]any{snap}, langs...)...)
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
	mods, err := db.QueryContext(ctx, `SELECT f.path, c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND f.language = 'go.mod'`, snap)
	if err != nil {
		return nil, nil, err
	}
	defer mods.Close()
	modules := map[string]string{}
	for mods.Next() {
		var p, content string
		if err := mods.Scan(&p, &content); err != nil {
			return nil, nil, err
		}
		if m := moduleOf(content); m != "" {
			modules[dir(p)] = m
		}
	}
	return files, modules, mods.Err()
}

// store replaces the snapshot's edges, storeBatch at a time so no
// transaction holds the write lock for long (a Go monorepo has millions),
// and stops once the snapshot is pruned or gone: prune would not come back
// for the rows. It returns how many it stored.
// ponytail: a crash between batches leaves part of the edges, which LinkStale
// takes for a linked snapshot; the repository's next index replaces them.
func store(ctx context.Context, db *sql.DB, snap int64, edges []edge) (int, error) {
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
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO file_edges(snapshot_id, src, dst) VALUES (?, ?, ?)`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()
	for _, e := range batch {
		if _, err := stmt.ExecContext(ctx, snap, e.src, e.dst); err != nil {
			return false, err
		}
	}
	return false, tx.Commit()
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
