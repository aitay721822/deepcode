// Package outline holds what a wiki's plan is made of: its pages, each with
// the files it covers, and what planning shows the model of a repository.
// Its subpackages shape the plan: place from the code's structure, contract
// from the model's replies, each held to the rules its request states.
package outline

import (
	"maps"
	"path"
	"slices"
	"strings"
)

// A planned page's title, and each of its search terms, in characters: a
// prompt-injected README could ask for one the size of a page, shown in the
// nav and the slug. A page has at most MaxTerms terms.
const (
	MaxTitle = 120
	MaxTerm  = 60
	MaxTerms = 12
)

// Page is a page to write: its title, its search terms, the files it covers
// (those the model chose for it, in its order, then those code placed beside
// them), the units it covers, by key, and its parent, the index of the
// section page it sits under, or -1 at the top.
type Page struct {
	Title  string
	Terms  []string
	Chosen []string
	Placed []string
	Units  []string
	Parent int
}

// Files are p's files, the chosen first.
func (p Page) Files() []string { return append(slices.Clone(p.Chosen), p.Placed...) }

// Query is p's terms as the query column holds them: joined by spaces.
func (p Page) Query() string { return strings.Join(p.Terms, " ") }

// Terms are terms on one line each, clipped to MaxTerm characters, the empty
// dropped, at most MaxTerms.
func Terms(terms []string) []string {
	var out []string
	for _, t := range terms {
		if t = Clip(OneLine(t), MaxTerm); t != "" && len(out) < MaxTerms {
			out = append(out, t)
		}
	}
	return out
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
