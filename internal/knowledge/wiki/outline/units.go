package outline

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/analyzer/markdown"
)

// perPage is how many units Spread adds to one page at most, so that what the
// model planned it on stays within the words the search keeps.
const perPage = 2

// Units are the subjects an aside section's pages name between them, the
// largest by lines first: each example (its folder under examples/), each
// guide, each folder of tests. Code is planned by what it declares and
// tooling by the tool, so neither has any.
func Units(s Section, lines map[string]int) []string {
	size := map[string]int{}
	var out []string
	for _, f := range s.Files {
		var u string
		switch s.Kind {
		case analyzer.Examples:
			u = example(f)
		case analyzer.Docs:
			u = f
		case analyzer.Tests:
			u = path.Dir(f)
		default:
			return nil
		}
		if _, ok := size[u]; !ok {
			out = append(out, u)
		}
		size[u] += lines[f]
	}
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(size[b], size[a]) })
	return out
}

// example is the folder of example f under examples/ or _examples/, or f
// itself when it lies there directly.
func example(f string) string {
	parts := strings.Split(f, "/")
	for i, p := range parts {
		if (p == "examples" || p == "_examples") && i+2 < len(parts) {
			return strings.Join(parts[:i+2], "/")
		}
	}
	return f
}

// Spread names in pages' queries the units that none names by its folder or
// file name, as a whole word: each, the largest first, in front of the query
// of the page given fewest, at most perPage to a page. A page writer gives
// each unit its focus names a heading of its own (writePrompt), so every
// example, guide and test suite is named somewhere, without a page of its
// own. Tests at the root, a unit ".", have no name and are left out.
func Spread(pages []Page, units []string) []Page {
	given := make([]int, len(pages))
	for _, u := range units {
		name := strings.ToLower(strings.TrimSuffix(path.Base(u), path.Ext(u)))
		if name == "" || len(pages) == 0 || slices.ContainsFunc(pages, func(p Page) bool { return named(p.Title+" "+p.Query, name) }) {
			continue
		}
		i := slices.Index(given, slices.Min(given))
		if given[i] == perPage {
			break
		}
		given[i]++
		pages[i].Query = Fit(short(u) + " " + pages[i].Query)
	}
	return pages
}

// named reports whether text holds name as a whole word, case aside, a word
// running on through letters, digits, "-" and "_": basic-auth names no
// example basic, nor rapid one api.
func named(text, name string) bool {
	text = strings.ToLower(text)
	word := func(i int) bool {
		if i < 0 || i >= len(text) {
			return false
		}
		c := text[i]
		return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_'
	}
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		if j += i; !word(j-1) && !word(j+len(name)) {
			return true
		}
		i = j + 1
	}
}

// short is unit u as a query names it: its last two parts, test/jmeter for
// src/test/jmeter, as the search keeps a query's first words only.
func short(u string) string {
	parts := strings.Split(u, "/")
	return strings.Join(parts[max(0, len(parts)-2):], "/")
}

// Recipes are the subjects of the sections of README text src, its # and ##
// headings, that show code, a fenced block before the next of those: each
// title up to its first ( ? ! : , or ;, "Transient updates" for zustand's
// "Transient updates (for often occurring state-changes)". A section of
// prose, features, credits or a licence, is no subject of the code's, nor is
// a ### one's, Steps or Optional, nor the README's # title, the project's
// name over its install line.
func Recipes(src string) []string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []string
	var walk func([]analyzer.Symbol)
	walk = func(ss []analyzer.Symbol) {
		for _, s := range ss {
			if strings.HasPrefix(s.Signature, "###") {
				continue
			}
			end := s.EndLine
			if i := slices.IndexFunc(s.Children, func(c analyzer.Symbol) bool { return !strings.HasPrefix(c.Signature, "###") }); i >= 0 {
				end = s.Children[i].StartLine - 1
			}
			own := strings.Join(lines[min(s.StartLine, len(lines)):min(end, len(lines))], "\n") // after its heading line
			title := strings.NewReplacer("`", "", "*", "").Replace(s.Name)
			if i := strings.IndexAny(title, "(?!:,;"); i >= 0 {
				title = title[:i]
			}
			if title = strings.TrimSpace(title); title != "" && (strings.Contains(own, "```") || strings.Contains(own, "~~~")) && !slices.Contains(out, title) {
				out = append(out, title)
			}
			walk(s.Children)
		}
	}
	secs := markdown.Sections([]byte(src))
	if len(secs) > 0 && strings.HasPrefix(secs[0].Signature, "# ") { // the title: its sections only
		walk(secs[0].Children)
		secs = secs[1:]
	}
	walk(secs)
	return out
}

// Readme names in pages' queries each of subjects, a README's recipes, that
// no page's search reaches (each of its retrieval.Terms among the page's, of
// its title and query: a recipe listed past them is read by none): in front
// of the query of the page naming most of its words, then given fewest,
// behind readme, the README's path, which the page search then reads, while
// the query has room, its search reaches the recipe and still reads every
// name it read (retrieval.FileNames: Cover's 16th is no 17th) and every
// recipe it reached, at most perPage a page. The query's other last words go
// past the search's terms as Spread's do, the recipe being the page's to
// explain. A page writer gives each subject its focus names a heading
// (writePrompt). The overview and a section's own page, which leave the
// details to its pages, take none; a subject with no word the search looks
// for (Go), none. It returns the subjects left for want of room.
func Readme(pages []Page, readme string, subjects []string) ([]Page, []string) {
	head := map[int]bool{0: true}
	for _, p := range pages {
		if p.Parent >= 0 {
			head[p.Parent] = true
		}
	}
	given := make([]int, len(pages))
	var left []string
	for _, s := range subjects {
		words := retrieval.Terms(s)
		count := func(p Page) int {
			n := 0
			for _, w := range words {
				if named(p.Title+" "+p.Query, w) {
					n++
				}
			}
			return n
		}
		if len(words) == 0 || slices.ContainsFunc(pages, func(p Page) bool { return reaches(p.Title, p.Query, s) }) {
			continue
		}
		with := func(q string) string { return readme + " " + s + " " + strings.TrimPrefix(q, readme+" ") }
		best, most := -1, -1
		for i, p := range pages {
			q := with(p.Query)
			if head[i] || given[i] == perPage || utf8.RuneCountInString(q) > MaxQuery || !reaches(p.Title, q, s) ||
				slices.ContainsFunc(retrieval.FileNames(p.Query), func(n string) bool { return !slices.Contains(retrieval.FileNames(q), n) }) ||
				slices.ContainsFunc(subjects, func(o string) bool { return reaches(p.Title, p.Query, o) && !reaches(p.Title, q, o) }) {
				continue
			}
			if n := count(p); n > most || n == most && given[i] < given[best] {
				best, most = i, n
			}
		}
		if best < 0 {
			left = append(left, s)
			continue
		}
		pages[best].Query = with(pages[best].Query)
		given[best]++
	}
	return pages, left
}

// reaches reports whether a page titled title searching query looks for each
// word of subject the search would (retrieval.Terms): none past its 16th.
func reaches(title, query, subject string) bool {
	terms := retrieval.Terms(title + "\n" + query)
	words := retrieval.Terms(subject)
	return len(words) > 0 && !slices.ContainsFunc(words, func(w string) bool { return !slices.Contains(terms, w) })
}
