package outline

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"deepcode/internal/repo/index/analyzer"
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
		pages[i].Query = Clip(short(u)+" "+pages[i].Query, MaxQuery)
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
