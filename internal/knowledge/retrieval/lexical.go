package retrieval

import (
	"context"
	"regexp"
	"strings"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/chunker"
)

var (
	wordRe  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	identRe = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]{2,}`)
)

var stopwords = toSet(`a about after all also an and any are as at be been but by can code could did do does
	each explain file files for from function functions get has have how i if in into is it its me more my
	not of on or our please show so some tell than that the their them then there these they this those to
	use used uses using was we what when where which while who why will with work works would you your`)

// toSet is the set of s's space-separated words.
func toSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// ftsQuery turns a question into an OR of at most 16 prefix terms, so
// "authentication token" also matches authenticateUser and tokens. The words
// of its compound identifiers follow its own words (userToken: user, token),
// so they never crowd out the question's own words; the terms column holds
// them for every identifier made of them, refreshUserToken's included.
func ftsQuery(q string) string {
	words := wordRe.FindAllString(q, -1)
	candidates := make([]string, 0, 2*len(words))
	for _, w := range words {
		candidates = append(candidates, strings.ToLower(w))
	}
	for _, w := range words {
		candidates = append(candidates, chunker.Split(w)...)
	}
	var terms []string
	seen := map[string]bool{}
	for _, w := range candidates {
		if len(w) < 3 || stopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"*`)
		if len(terms) == 16 {
			break
		}
	}
	return strings.Join(terms, " OR ")
}

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

// fts ranks the snapshot's chunks by BM25 (porter-stemmed), weighting symbol
// names above paths and content, the words of compound identifiers (terms)
// below them, and source code above tests and docs. Given paths, it ranks
// only the chunks of those files. The weights come from a sweep over
// test/eval's corpora (README, 檢索評測).
// ponytail: MATCH spans every snapshot and the join filters afterwards; give
// chunk_fts a snapshot column once many repositories share one database.
func (r *Retriever) fts(ctx context.Context, snapshotID int64, query string, paths []string, limit int) ([]int64, error) {
	m := ftsQuery(query)
	if m == "" {
		return nil, nil
	}
	in, args := "", []any{m, snapshotID}
	if len(paths) > 0 {
		in = " AND c.path IN (" + storage.Placeholders(len(paths)) + ")"
		for _, p := range paths {
			args = append(args, p)
		}
	}
	// bm25 is negative (lower is better), so the 0.25 weight moves a chunk down.
	return scanIDs(r.DB.QueryContext(ctx, `
		SELECT c.id FROM chunk_fts JOIN chunks c ON c.id = chunk_fts.rowid
		WHERE chunk_fts MATCH ? AND c.snapshot_id = ?`+in+`
		ORDER BY bm25(chunk_fts, 1.0, 6.0, 1.0, 0.5) * `+secondary+` LIMIT ?`, append(args, limit)...))
}

// symbolChunks finds chunks holding the definition of any of the first 32
// identifiers named in the query, longest (most specific) names first. Case
// does not count, except for members named like the nouns of a question (C#'s
// Items, Order, Status properties) and constructors (their class's name): only
// a query spelling them as the code does finds those.
func (r *Retriever) symbolChunks(ctx context.Context, snapshotID int64, query string) ([]int64, error) {
	var names []any
	seen := map[string]bool{}
	for _, w := range identRe.FindAllString(query, -1) {
		lw := strings.ToLower(w)
		if stopwords[lw] || seen[lw] {
			continue
		}
		seen[lw] = true
		names = append(names, w)
		if len(names) == 32 { // a bounded IN list, as ftsQuery bounds its terms
			break
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	in := storage.Placeholders(len(names))
	args := append(append(append([]any{snapshotID}, names...), names...), symbolLimit)
	return scanIDs(r.DB.QueryContext(ctx, `
		SELECT c.id FROM symbols s
		JOIN chunks c ON c.snapshot_id = s.snapshot_id AND c.path = s.path
			AND c.start_line <= s.start_line AND c.end_line >= s.start_line
		WHERE s.snapshot_id = ? AND s.name COLLATE NOCASE IN (`+in+`)
			AND (s.kind NOT IN ('property', 'event', 'indexer', 'constructor') OR s.name IN (`+in+`))
		GROUP BY c.id ORDER BY max(length(s.name)) DESC, c.id LIMIT ?`, args...))
}
