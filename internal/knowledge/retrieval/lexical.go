package retrieval

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"deepcode/internal/knowledge/retrieval/names"
	"deepcode/internal/repo/index/chunker"
)

var (
	wordRe  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	identRe = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]{2,}`)
)

// The files a query names: at most maxFiles of them, those of a page's
// focus, not a whole folder of examples; of their chunks, at most namedPerFile
// a file and namedLimit in all go to the fusion. They rank there as FTS's own
// do, as what the query names is what it asks for, and a lower weight let
// every project's Extensions.cs, found by the symbol search too, push eShop's
// AppHost.cs out of its page again; a question naming package.json gives it
// three of ten places at most, and the code it asks about the rest. Each
// file's best chunk is sure of a place (fusion.Sure), as fusion ranks a chunk
// only the named list finds below most chunks two searches find.
const (
	maxFiles     = 40
	namedPerFile = 3
	namedLimit   = 6
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

// ftsQuery turns a question into an OR of its Terms as prefixes, so
// "authentication token" also matches authenticateUser and tokens.
func ftsQuery(q string) string {
	terms := Terms(q)
	for i, t := range terms {
		terms[i] = `"` + t + `"*`
	}
	return strings.Join(terms, " OR ")
}

// Terms are the words a search looks for of query, at most 16, lowercased:
// what a query asks past them, the search does not see. The words of its
// compound identifiers follow its own words (userToken: user, token), so they
// never crowd out the question's own words; the terms column holds them for
// every identifier made of them, refreshUserToken's included.
func Terms(q string) []string {
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
		terms = append(terms, w)
		if len(terms) == 16 {
			break
		}
	}
	return terms
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

// named ranks the chunks of the files the query names (names.FileNames): the files
// named first (pick), each file's best chunk by the query's words first
// (byFile), and returns how many lead it, one a file. A wiki page's focus
// lists the files it is about, the main ones and the examples placed in it
// first, and their words alone may rank them nowhere, as eShop's AppHost.cs
// under every project's Extensions.cs.
func (r *Retriever) named(ctx context.Context, snapshotID int64, query string) ([]int64, int, error) {
	listed := names.FileNames(query)
	if len(listed) == 0 {
		return nil, 0, nil
	}
	groups, err := r.Store.Files(ctx, snapshotID, listed, maxFiles)
	files := slices.Concat(groups...)
	if err != nil || len(files) == 0 {
		return nil, 0, err
	}
	ids, err := r.fts(ctx, snapshotID, query, files, ftsLimit)
	if err != nil {
		return nil, 0, err
	}
	hits, err := r.Store.Chunks(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	picked := pick(groups, hits)
	// A picked file none of the query's words find has its first chunk read.
	found := map[string]bool{}
	for _, h := range hits {
		found[h.Path] = true
	}
	missing := slices.DeleteFunc(slices.Clone(picked), func(f string) bool { return found[f] })
	if len(missing) > 0 {
		first, err := r.Store.First(ctx, snapshotID, missing)
		if err != nil {
			return nil, 0, err
		}
		more, err := r.Store.Chunks(ctx, first)
		if err != nil {
			return nil, 0, err
		}
		hits = append(hits, more...)
	}
	ids, firsts := byFile(picked, hits)
	return ids, firsts, nil
}

// pick are the files the named list reads, at most namedLimit, of groups
// (each name's files, in the query's order): each name's next file in turn,
// a name's files by their best of the ranked hits, those no word finds last.
// What a query names first is what it is about, not what its words match
// best: a page's focus that lists two examples ahead of seven files of a
// third reads the two.
func pick(groups [][]string, hits []Hit) []string {
	rank := map[string]int{}
	for i, h := range hits {
		if _, ok := rank[h.Path]; !ok {
			rank[h.Path] = i
		}
	}
	at := func(f string) int {
		if i, ok := rank[f]; ok {
			return i
		}
		return len(hits)
	}
	for _, g := range groups {
		slices.SortStableFunc(g, func(a, b string) int { return at(a) - at(b) })
	}
	var out []string
	for k := 0; len(out) < namedLimit; k++ {
		n := len(out)
		for _, g := range groups {
			if k < len(g) && len(out) < namedLimit {
				out = append(out, g[k])
			}
		}
		if len(out) == n {
			break
		}
	}
	return out
}

// byFile is the ids of hits of files, in the order of files, each file's
// best ahead of any file's second, and so on, at most namedPerFile a file and
// namedLimit in all: a file whose words match best does not push the others'
// best chunks out of reach, nor fill the list alone. It also returns how
// many lead the list, the files' best.
func byFile(files []string, hits []Hit) ([]int64, int) {
	of := map[string][]int64{}
	for _, h := range hits {
		of[h.Path] = append(of[h.Path], h.ChunkID)
	}
	ids, firsts := make([]int64, 0, namedLimit), 0
	for k := range namedPerFile {
		for _, f := range files {
			if k < len(of[f]) && len(ids) < namedLimit {
				ids = append(ids, of[f][k])
			}
		}
		if k == 0 {
			firsts = len(ids)
		}
	}
	return ids, firsts
}

// symbolChunks finds chunks holding the definition of any of the first 32
// identifiers named in the query, or a method or function named by one of the
// first 16 runs of two or three adjacent words joined (route pattern:
// RoutePattern); Store.Defining ranks them, the names the query writes as
// code (coded) first, and says which symbols count, and when case does.
func (r *Retriever) symbolChunks(ctx context.Context, snapshotID int64, query string) ([]int64, error) {
	names := bounded(identRe.FindAllString(query, -1), 32, nil)
	runs := bounded(joined(query), 16, names)
	if len(names)+len(runs) == 0 {
		return nil, nil
	}
	return r.Store.Defining(ctx, snapshotID, names, coded(query, names), runs, symbolLimit)
}

// coded is those of names the query writes as code somewhere: in mixed case
// with a capital inside (fromJson, HTTPServer, not JSON), with _, $ or a
// digit, or beside a backtick, an opening parenthesis, :: or #, or a dot
// joining it to another name (Gson.fromJson, not a sentence's end).
func coded(query string, names []string) []string {
	name := func(b byte) bool {
		return b < 0x80 && (b == '_' || b == '$' || unicode.IsLetter(rune(b)) || unicode.IsDigit(rune(b)))
	}
	code := map[string]bool{}
	for _, m := range identRe.FindAllStringIndex(query, -1) {
		w, before, after := query[m[0]:m[1]], query[:m[0]], query[m[1]:]
		mixed := strings.ToLower(w) != w && strings.ToUpper(w) != w && strings.ContainsFunc(w[1:], unicode.IsUpper)
		if mixed || strings.ContainsAny(w, "_$0123456789") ||
			strings.HasSuffix(before, "`") || strings.HasPrefix(after, "`") || strings.HasPrefix(after, "(") ||
			strings.HasSuffix(before, "::") || strings.HasPrefix(after, "::") || strings.HasSuffix(before, "#") || strings.HasPrefix(after, "#") ||
			strings.HasSuffix(before, ".") && len(before) > 1 && name(before[len(before)-2]) ||
			strings.HasPrefix(after, ".") && len(after) > 1 && name(after[1]) {
			code[strings.ToLower(w)] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !code[strings.ToLower(n)] })
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
