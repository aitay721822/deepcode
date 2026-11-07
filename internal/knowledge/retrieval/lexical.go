package retrieval

import (
	"context"
	"regexp"
	"slices"
	"strings"

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

// fts ranks the snapshot's chunks by BM25 for the words of query
// (Store.FTS), only those of the files in paths when there are any.
func (r *Retriever) fts(ctx context.Context, snapshotID int64, query string, paths []string, limit int) ([]int64, error) {
	m := ftsQuery(query)
	if m == "" {
		return nil, nil
	}
	return r.Store.FTS(ctx, snapshotID, m, paths, limit)
}

// symbolChunks finds chunks holding the definition of any of the first 32
// identifiers named in the query, or a method or function named by one of the
// first 16 runs of two or three adjacent words joined (route pattern:
// RoutePattern), longest (most specific) names first; Store.Defining says
// which symbols count, and when case does.
func (r *Retriever) symbolChunks(ctx context.Context, snapshotID int64, query string) ([]int64, error) {
	names := bounded(identRe.FindAllString(query, -1), 32, nil)
	runs := bounded(joined(query), 16, names)
	if len(names)+len(runs) == 0 {
		return nil, nil
	}
	return r.Store.Defining(ctx, snapshotID, names, runs, symbolLimit)
}

// bounded is the first limit of words that are not stopwords, each once and
// none already in taken: a bounded IN list, as ftsQuery bounds its terms.
func bounded(words []string, limit int, taken []string) []string {
	seen := map[string]bool{}
	for _, t := range taken {
		seen[strings.ToLower(t)] = true
	}
	var out []string
	for _, w := range words {
		if lw := strings.ToLower(w); !stopwords[lw] && !seen[lw] && len(out) < limit {
			seen[lw] = true
			out = append(out, w)
		}
	}
	return out
}

// joined is the query's runs of two and three adjacent words, no stopword
// among them, each written as one word: how a question spells an identifier
// it does not know (matched route pattern: matchedroute, matchedroutepattern,
// routepattern).
func joined(query string) []string {
	words := wordRe.FindAllString(query, -1)
	var out []string
	for i := range words {
		for n := 2; n <= 3 && i+n <= len(words); n++ {
			run := words[i : i+n]
			if slices.ContainsFunc(run, func(w string) bool { return stopwords[strings.ToLower(w)] }) {
				break
			}
			out = append(out, strings.Join(run, ""))
		}
	}
	return out
}
