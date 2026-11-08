package retrieval

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/chunker"
)

var (
	wordRe  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	identRe = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]{2,}`)
	// lineRe matches the line a word names after its file: a.go:42, a.go#L42-L50.
	lineRe = regexp.MustCompile(`(?:(?::\d+)+(?:-\d+)?|#L\d+(?:-L?\d+)?)$`) // a.go:42:7 too
)

// The files a query names: at most maxFiles of them, those of a page's
// focus, not a whole folder of examples; of their chunks, at most namedPerFile
// a file and namedLimit in all go to the fusion. They rank there as FTS's own
// do, as what the query names is what it asks for, and a lower weight let
// every project's Extensions.cs, found by the symbol search too, push eShop's
// AppHost.cs out of its page again; a question naming package.json gives it
// three of ten places at most, and the code it asks about the rest.
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

// named ranks, by the query's words, the chunks of the files the query names
// (fileNames), each file's best first (byFile): a wiki page's focus lists the
// files it is about, and their words alone may rank them nowhere, as eShop's
// AppHost.cs under every project's Extensions.cs.
func (r *Retriever) named(ctx context.Context, snapshotID int64, query string) ([]int64, error) {
	names := fileNames(query)
	if len(names) == 0 {
		return nil, nil
	}
	files, err := r.Store.Files(ctx, snapshotID, names, maxFiles)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	ids, err := r.fts(ctx, snapshotID, query, files, ftsLimit)
	if err != nil {
		return nil, err
	}
	hits, err := r.Store.Chunks(ctx, ids)
	if err != nil {
		return nil, err
	}
	// A file none of the query's first words find, as one listed late in a
	// long focus, still has its first chunk read, while places are left.
	var missing []string
	found := map[string]bool{}
	for _, h := range hits {
		found[h.Path] = true
	}
	for _, f := range files {
		if !found[f] && len(found)+len(missing) < namedLimit {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		first, err := r.Store.First(ctx, snapshotID, missing)
		if err != nil {
			return nil, err
		}
		more, err := r.Store.Chunks(ctx, first)
		if err != nil {
			return nil, err
		}
		hits = append(hits, more...)
	}
	return byFile(hits), nil
}

// byFile is the ids of ranked hits, each file's first ahead of any file's
// second, and so on, at most namedPerFile a file and namedLimit in all: a
// file whose words match best does not push the others' best chunks out of
// reach, nor fill the list alone.
func byFile(hits []Hit) []int64 {
	nth := map[string]int{}
	var order []int // of hits, by the file's how-manyth chunk each is
	rank := make([]int, len(hits))
	for i, h := range hits {
		if rank[i] = nth[h.Path]; rank[i] < namedPerFile {
			order = append(order, i)
		}
		nth[h.Path]++
	}
	slices.SortStableFunc(order, func(a, b int) int { return rank[a] - rank[b] })
	ids := make([]int64, 0, namedLimit)
	for _, j := range order[:min(len(order), namedLimit)] {
		ids = append(ids, hits[j].ChunkID)
	}
	return ids
}

// fileNames are the first 16 words of query that may name files or folders,
// each once: a path (src/app.ts, examples/, app/[slug]), a glob (tests/*.rs)
// or the name of a file of an indexed language (App.java, go.mod; not
// res.send or .NET), never a URL. Quotes, full-width punctuation and stops
// around a word go, as do markdown's bold, brackets wrapping it or left
// unpaired, the line after it (a.go:42, a.go#L42) and a leading ./ or /.
func fileNames(query string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",;`'\"|=<>，、；：。？！（）「」『』《》", r)
	}) {
		w = strings.ReplaceAll(w, `\`, "/")
		for prev := ""; prev != w; { // **a.go**. as much as **a.go.**
			prev, w = w, strings.TrimRight(strings.Trim(w, "*"), ".:!?")
		}
		w = unwrap(w)
		w = strings.TrimLeft(strings.TrimPrefix(strings.TrimRight(lineRe.ReplaceAllString(w, ""), ".:!?"), "./"), "/")
		_, indexed := analyzer.Detect(w)
		if w != "" && (strings.Contains(w, "/") || indexed) && !strings.Contains(w, "://") && !slices.Contains(out, w) && len(out) < 16 {
			out = append(out, w)
		}
	}
	return out
}

// unwrap drops the brackets around w, and one at an end that w leaves
// unpaired: (src/a.ts) and src/a.ts) are src/a.ts, app/[slug],
// (group)/page.tsx and (group)/(admin) stay.
func unwrap(w string) string {
	for _, p := range []string{"()", "[]"} {
		open, close := p[:1], p[1:]
		for len(w) >= 2 && strings.HasPrefix(w, open) && strings.HasSuffix(w, close) && balanced(w[1:len(w)-1], open[0], close[0]) {
			w = w[1 : len(w)-1]
		}
		if strings.HasSuffix(w, close) && strings.Count(w, close) > strings.Count(w, open) {
			w = w[:len(w)-1]
		}
		if strings.HasPrefix(w, open) && strings.Count(w, open) > strings.Count(w, close) {
			w = w[1:]
		}
	}
	return w
}

// balanced reports whether every close in s follows an open it closes, and
// every open is closed.
func balanced(s string, open, close byte) bool {
	depth := 0
	for i := range len(s) {
		switch s[i] {
		case open:
			depth++
		case close:
			if depth--; depth < 0 {
				return false
			}
		}
	}
	return depth == 0
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
