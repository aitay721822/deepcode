package store

import (
	"context"
	"strings"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/analyzer"
)

// secondary is the SQL weight that quarters the BM25 score of tests, docs,
// examples and generated files: they repeat a question's words more often
// than the code that implements them, and would otherwise crowd it out.
var secondary = "CASE WHEN " + globs("c.path", analyzer.SecondaryGlobs...) +
	" OR EXISTS (SELECT 1 FROM source_files f WHERE f.snapshot_id = c.snapshot_id AND f.path = c.path AND f.generated)" +
	" THEN 0.25 ELSE 1.0 END"

// globs is the SQL condition that column matches any of patterns.
func globs(column string, patterns ...string) string {
	conds := make([]string, len(patterns))
	for i, p := range patterns {
		conds[i] = column + " GLOB '" + p + "'"
	}
	return strings.Join(conds, " OR ")
}

// FTS returns up to limit of the snapshot's chunks that match the FTS5 query
// match, ranked by BM25 (porter-stemmed), weighting symbol names above paths
// and content, the words of compound identifiers (terms) below them, and
// source code above tests and docs. Given paths, it ranks only the chunks of
// those files. The weights come from a sweep over test/eval's corpora
// (README, 檢索評測).
// ponytail: MATCH spans every snapshot and the join filters afterwards; give
// chunk_fts a snapshot column once many repositories share one database.
func (s *Store) FTS(ctx context.Context, snapshotID int64, match string, paths []string, limit int) ([]int64, error) {
	in, args := "", []any{match, snapshotID}
	if len(paths) > 0 {
		in = " AND c.path IN (" + storage.Placeholders(len(paths)) + ")"
		for _, p := range paths {
			args = append(args, p)
		}
	}
	// bm25 is negative (lower is better), so the 0.25 weight moves a chunk down.
	return scanIDs(s.DB.QueryContext(ctx, `
		SELECT c.id FROM chunk_fts JOIN chunks c ON c.id = chunk_fts.rowid
		WHERE chunk_fts MATCH ? AND c.snapshot_id = ?`+in+`
		ORDER BY bm25(chunk_fts, 1.0, 6.0, 1.0, 0.5) * `+secondary+` LIMIT ?`, append(args, limit)...))
}

// Defining returns up to limit chunks holding the definition of a symbol
// named one of names, or of a method or function named one of runs, longest
// (most specific) names first. A run naming a type is left out: catalog item
// or integration event are a question's nouns, and their classes rarely
// answer it. Case does not count, except for members named like the nouns of
// a question (C#'s Items, Order, Status properties) and constructors (their
// class's name): only a name spelled as the code does finds those.
func (s *Store) Defining(ctx context.Context, snapshotID int64, names, runs []string, limit int) ([]int64, error) {
	in, run := storage.Placeholders(len(names)), storage.Placeholders(len(runs))
	args := []any{snapshotID}
	for _, list := range [][]string{names, names, runs} {
		for _, n := range list {
			args = append(args, n)
		}
	}
	return scanIDs(s.DB.QueryContext(ctx, `
		SELECT c.id FROM symbols s
		JOIN chunks c ON c.snapshot_id = s.snapshot_id AND c.path = s.path
			AND c.start_line <= s.start_line AND c.end_line >= s.start_line
		WHERE s.snapshot_id = ? AND (s.name COLLATE NOCASE IN (`+in+`)
				AND (s.kind NOT IN ('property', 'event', 'indexer', 'constructor') OR s.name IN (`+in+`))
			OR s.name COLLATE NOCASE IN (`+run+`) AND s.kind IN ('method', 'function'))
		GROUP BY c.id ORDER BY max(length(s.name)) DESC, c.id LIMIT ?`, append(args, limit)...))
}
