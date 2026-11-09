// Package outline shapes a wiki before it is written: how many pages a
// repository's size calls for, the sections its code divides into (the
// directories that import each other most), and the pages the model plans.
package outline

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strings"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/graph"
)

// A planned page's title and query, in characters: a prompt-injected README
// could ask for one the size of a page, shown in the nav and the slug. A
// query holds the 16 names its search reads (retrieval.FileNames) at a .NET
// or Java project's paths, 85 characters at most for nine in ten: at 500,
// eShop's Ordering pages named six files each, cut off mid-path.
const (
	MaxTitle = 120
	MaxQuery = 1400
)

// Page is a page to write: its title, what to search the code for, and its
// parent, the index of the section page it sits under, or -1 at the top.
type Page struct {
	Title  string `json:"title"`
	Query  string `json:"query"`
	Parent int    `json:"-"`
}

// NewPage is a top-level page titled title, its words on one line and both
// clipped; ok is false for a title with no words.
func NewPage(title, query string) (p Page, ok bool) {
	p = Page{Title: Clip(OneLine(title), MaxTitle), Query: Fit(query), Parent: -1}
	return p, p.Title != ""
}

// Fit is query without the separators it starts with, clipped to MaxQuery
// characters before the word the cut falls in, a word ending at what
// retrieval.FileNames splits names at (a space, comma, full-width stop…): a
// path cut short (…/CreateOrderDraftComm) would take one of the 16 names the
// page search reads and name nothing; the separators it then ends with go
// too. The cut word goes however long it is, unless no separator comes
// before it: then the query is cut where the cut falls.
func Fit(query string) string {
	query = strings.TrimLeftFunc(query, retrieval.Separates)
	r := []rune(query)
	if len(r) <= MaxQuery {
		return query
	}
	kept := string(r[:MaxQuery])
	if i := strings.LastIndexFunc(kept, retrieval.Separates); i >= 0 && !retrieval.Separates(r[MaxQuery]) {
		kept = kept[:i]
	}
	return strings.TrimRightFunc(kept, retrieval.Separates)
}

// OneLine joins the words of s with single spaces: a title goes into the
// job's output, where a line break would start a line of its own, and text
// from the repository into prompts, where a blank line could seem to end the
// block that frames it.
func OneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Clip keeps s's first n characters.
func Clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Size is how many pages a wiki gets for a repository of files indexed
// files, at most maxPages, and in how many sections: 0 below 12 pages, which
// are one flat list. ponytail: a linear guess at what DeepWiki writes; size
// by symbols or modules if pages come out thin or crowded.
func Size(files, maxPages int) (pages, sections int) {
	pages = max(1, min(maxPages, min(files, 16)+files/4))
	if pages < 12 {
		return pages, 0
	}
	return pages, min(12, max(2, (pages+3)/6))
}

// Section is a part of the repository a section of the wiki covers.
type Section struct {
	Kind  string   // "" for code, else what analyzer.Aside names: tests, examples, docs or tooling
	Dirs  []string // largest first; "." is the root; none for an aside
	Files []string
}

// maxUnits bounds the directories Sections merges, as each merge weighs
// every pair. ponytail: past it the smallest directories go in as one; merge
// with a heap if monorepos of thousands of packages plan badly.
const maxUnits = 150

// asides are the kinds of files outside the code, in the order their sections
// follow the code's.
var asides = []string{analyzer.Docs, analyzer.Examples, analyzer.Tests, analyzer.Tooling}

// Sections groups files by their part of the code (parts maps each to its
// directory, "" for a test, example, doc or hidden file) into about n
// sections: the directories that import each other most per file go
// together, then those whose largest folders share a parent, then the
// smallest. The docs, examples, tests and tooling then follow in a section
// each; tooling takes a directory of only build and configuration files
// (analyzer.Config) too, so a manifest is no section of its own, but a root
// README as long as a long guide (lines holds each file's) is a guide: the
// overview reads its start only. The code's sections go largest first.
func Sections(parts map[string]string, edges []graph.DirEdge, lines map[string]int, n int) []Section {
	byDir, aside := map[string][]string{}, map[string][]string{}
	for f, d := range parts {
		if d == "" {
			k := cmp.Or(analyzer.Aside(f), analyzer.Docs) // what Parts set aside, under a name of its own
			if k == analyzer.Tooling && !strings.Contains(f, "/") && strings.HasPrefix(strings.ToLower(f), "readme") && lines[f] >= 2*linesPerPage {
				k = analyzer.Docs
			}
			aside[k] = append(aside[k], f)
		} else {
			byDir[d] = append(byDir[d], f)
		}
	}
	for d, files := range byDir {
		if !slices.ContainsFunc(files, func(f string) bool { return !analyzer.Config(f) }) {
			aside[analyzer.Tooling] = append(aside[analyzer.Tooling], files...)
			delete(byDir, d)
		}
	}
	var tail []Section
	for _, k := range asides {
		if files := aside[k]; len(files) > 0 {
			slices.Sort(files)
			tail = append(tail, Section{Kind: k, Files: files})
		}
	}
	n = max(1, n-len(tail))
	dirs := slices.Sorted(func(yield func(string) bool) {
		for d := range byDir {
			if !yield(d) {
				return
			}
		}
	})
	slices.SortStableFunc(dirs, func(a, b string) int { return len(byDir[b]) - len(byDir[a]) })
	index := map[string]int{}
	var out []Section
	var lead [][]string // each section's largest directory, by folder
	for i, d := range dirs {
		slices.Sort(byDir[d])
		if i < maxUnits {
			out, lead = append(out, Section{}), append(lead, strings.Split(d, "/"))
		}
		at := len(out) - 1 // the last unit takes in the smallest directories past maxUnits
		index[d] = at
		out[at].Dirs, out[at].Files = append(out[at].Dirs, d), append(out[at].Files, byDir[d]...)
	}
	w := make([][]float64, len(out)) // imports between sections, both ways
	for i := range w {
		w[i] = make([]float64, len(out))
	}
	for _, e := range edges {
		if i, ok := index[e.From]; ok {
			if j, ok := index[e.To]; ok && i != j {
				w[i][j] += float64(e.Count)
				w[j][i] += float64(e.Count)
			}
		}
	}
	for len(out) > max(n, 1) {
		bi, bj, best := 0, 1, -1.0
		for i := range out {
			for j := i + 1; j < len(out); j++ {
				if s := (w[i][j] + kin(lead[i], lead[j])) / float64(len(out[i].Files)+len(out[j].Files)); s > best {
					bi, bj, best = i, j, s
				}
			}
		}
		if len(out[bj].Files) > len(out[bi].Files) {
			lead[bi] = lead[bj]
		}
		out[bi].Dirs, out[bi].Files = append(out[bi].Dirs, out[bj].Dirs...), append(out[bi].Files, out[bj].Files...)
		for k := range w {
			w[bi][k] += w[bj][k]
			w[k][bi] = w[bi][k]
		}
		w[bi][bi] = 0
		out, w, lead = slices.Delete(out, bj, bj+1), slices.Delete(w, bj, bj+1), slices.Delete(lead, bj, bj+1)
		for k := range w {
			w[k] = slices.Delete(w[k], bj, bj+1)
		}
	}
	for i := range out {
		slices.SortStableFunc(out[i].Dirs, func(a, b string) int {
			return len(byDir[b]) - len(byDir[a])
		})
		slices.Sort(out[i].Files)
	}
	slices.SortStableFunc(out, func(a, b Section) int { return len(b.Files) - len(a.Files) })
	return append(out, tail...)
}

// kin is how near two directories, given by folder, are: half an import for
// each leading folder they share, so siblings merge before strangers when no
// import links them.
func kin(a, b []string) float64 {
	n := 0
	for n < min(len(a), len(b)) && a[n] == b[n] && a[n] != "." {
		n++
	}
	return float64(n) / 2
}

// Fits reports whether a wiki, sectioned or flat, still has the shape Size
// gives a repository of files indexed files: a new snapshot carries over only
// a wiki that does.
func Fits(sectioned bool, files, maxPages int) bool {
	_, n := Size(files, maxPages)
	return sectioned == (n > 0)
}

// maxLines is the most lines a file weighs in Budgets.
const maxLines = 2000

// linesPerPage is the fewest weighed lines a section needs for each page past
// its first: about what one of DeepWiki's pages covers, so that a section of
// many short files is not spread over pages that each retell the same code.
const linesPerPage = 100

// light are the languages of the code's styles and data, which weigh less.
var light = []string{"css", "scss", "less", "sql"}

// Budgets shares pages out among sections, a section's own page among them:
// one each, then one for each of a section's directories, round by round, and
// then each next page to the section with the fewest for its weight: its
// lines (lines holds each file's; one it lacks counts one, and none more than
// maxLines, as a bundled file need not be marked generated), so a core of a
// few long files is not outweighed by many short ones, though a stylesheet,
// SQL or a configuration file in the code weighs a quarter. No section takes
// more than three times its files of code and one, nor a page for less than
// linesPerPage; docs and examples weigh a third, as they show the code rather
// than being it, and take a third and a fifth of the pages at most, though
// docs a page for each long guide first (up to three: a guide is a topic of
// its own); tests and tooling take one page in twelve at most, four and three,
// so together no more than a fifth: what a reader comes for is the code.
func Budgets(sections []Section, lines map[string]int, pages int) []int {
	n := len(sections)
	out, weight, limit, floor := make([]int, n), make([]float64, n), make([]int, n), make([]int, n)
	for i, s := range sections {
		code := 0
		for _, f := range s.Files {
			w := float64(min(max(lines[f], 1), maxLines))
			if lang, _ := analyzer.Detect(f); s.Kind == "" && (analyzer.Config(f) || slices.Contains(light, lang)) {
				w /= 4
			} else {
				code++
			}
			weight[i] += w
		}
		limit[i] = min(3*max(code, 1)+1, 1+int(weight[i])/linesPerPage)
		out[i], floor[i] = 1, min(limit[i], max(1, len(s.Dirs)+1))
		if len(s.Dirs) < 2 {
			floor[i] = 1
		}
		switch s.Kind {
		case analyzer.Docs:
			weight[i] /= 3
			limit[i] = min(limit[i], max(1, pages/3))
			long := 0
			for _, f := range s.Files {
				if lines[f] >= 2*linesPerPage {
					long++
				}
			}
			floor[i] = min(limit[i], max(1, long), 3)
		case analyzer.Examples:
			weight[i] /= 3
			limit[i] = min(limit[i], max(1, pages/5))
		case analyzer.Tests:
			limit[i] = min(limit[i], 4, max(1, pages/12))
		case analyzer.Tooling:
			limit[i] = min(limit[i], 3, max(1, pages/12))
		}
	}
	left := pages - n
	for more := true; more && left > 0; {
		more = false
		for i := range out {
			if out[i] < floor[i] && left > 0 {
				out[i], left, more = out[i]+1, left-1, true
			}
		}
	}
	for ; left > 0; left-- {
		best := -1
		for i := range out {
			if out[i] < limit[i] && (best < 0 || float64(out[i])/weight[i] < float64(out[best])/weight[best]) {
				best = i
			}
		}
		if best < 0 {
			break // every section has all it may take
		}
		out[best]++
	}
	return out
}

// Pick is at most n of files to show the model: the longest of each
// directory in turn, the directories with the most lines first, so that no
// directory goes unseen for a sibling's many files (alphabetical order would
// show axum-core/ and leave axum/ out); in path order.
func Pick(files []string, lines map[string]int, n int) []string {
	if len(files) <= n {
		return files
	}
	byDir, total := map[string][]string{}, map[string]int{}
	for _, f := range files {
		d := path.Dir(f)
		byDir[d], total[d] = append(byDir[d], f), total[d]+lines[f]
	}
	dirs := slices.Sorted(maps.Keys(byDir))
	slices.SortStableFunc(dirs, func(a, b string) int { return total[b] - total[a] })
	for _, d := range dirs {
		slices.SortStableFunc(byDir[d], func(a, b string) int { return lines[b] - lines[a] })
	}
	var out []string
	for i := 0; len(out) < n; i++ {
		for _, d := range dirs {
			if i < len(byDir[d]) && len(out) < n {
				out = append(out, byDir[d][i])
			}
		}
	}
	slices.Sort(out)
	return out
}

// ParsePages reads the model's JSON plan, {"pages":[{"title","query"}]},
// tolerating reasoning blocks and prose around it, and keeps at most limit
// titled pages, their titles and queries clipped.
func ParsePages(s string, limit int) []Page {
	var v struct {
		Pages []Page `json:"pages"`
	}
	if !ai.ParseJSON(s, &v) {
		return nil
	}
	return keep(v.Pages, limit)
}

// ParseHeads reads the model's outline, {"overview":{...},"sections":[...]},
// as ParsePages reads pages: the overview, ok false without one, and the first
// limit section pages in order, one the model left untitled kept in its place
// with no title, so that each stays with its section.
func ParseHeads(s string, limit int) (overview Page, sections []Page, ok bool) {
	var v struct {
		Overview Page   `json:"overview"`
		Sections []Page `json:"sections"`
	}
	if !ai.ParseJSON(s, &v) {
		return Page{}, nil, false
	}
	for _, p := range v.Sections[:min(limit, len(v.Sections))] {
		head, _ := NewPage(p.Title, p.Query)
		sections = append(sections, head)
	}
	overview, ok = NewPage(v.Overview.Title, v.Overview.Query)
	return overview, sections, ok
}

// keep is the first limit pages that have a title, made top-level pages.
func keep(pages []Page, limit int) []Page {
	var out []Page
	for _, p := range pages {
		if p, ok := NewPage(p.Title, p.Query); ok && len(out) < limit {
			out = append(out, p)
		}
	}
	return out
}
