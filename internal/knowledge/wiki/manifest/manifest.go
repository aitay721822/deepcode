// Package manifest knows the files that list a project's dependencies and
// build, which change often and which a wiki page cites a few lines of: a
// change to one touches only the pages citing the lines it replaced.
package manifest

import (
	"path"
	"slices"
	"strings"
	"unicode"
)

// names are the manifests known by name, in lower case.
var names = map[string]bool{"package.json": true, "go.mod": true, "cargo.toml": true, "pom.xml": true,
	"build.gradle": true, "build.gradle.kts": true, "settings.gradle": true, "settings.gradle.kts": true,
	"directory.build.props": true, "directory.packages.props": true, "packages.config": true, "global.json": true}

// Is reports whether file p is a manifest or a .NET project or solution file.
func Is(p string) bool {
	name := strings.ToLower(path.Base(p))
	return names[name] || slices.Contains([]string{".csproj", ".vbproj", ".fsproj", ".sln", ".slnx"}, path.Ext(name))
}

// Touches reports whether a change touches a citation of lines first to last
// of file p on a page reading text: for a manifest, when the lines its edit
// replaced (edits holds them by file) share one with the citation and the
// page says a word they lost; for any other file, when the change affected it.
func Touches(p string, first, last int, text string, affected map[string]bool, edits map[string]Edit) bool {
	if !Is(p) {
		return affected[p]
	}
	e, ok := edits[p]
	text = strings.ToLower(text) // "Tokio" names tokio
	return ok && e.Covers(first, last) && (e.Gone == nil || slices.ContainsFunc(e.Gone, func(w string) bool { return strings.Contains(text, strings.ToLower(w)) }))
}

// Edit is what a change did to a manifest's base version: the lines it
// replaced and, when the lines after them stay in place, the words each of
// those lost. A page citing them reads differently only where it says one, as
// the version a bump replaced; one citing the dependency list as such stays.
// ponytail: words, not meaning; a page naming the old version another way
// ("v2.3") keeps its text.
type Edit struct {
	Lines
	Gone []string // nil: the lines after moved, so every citation of them is stale; empty: no word was lost
}

// Diff is the edit after makes to before.
func Diff(before, after string) Edit {
	l := Replaced(before, after)
	old, cur := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(old) != len(cur) || l.First < 1 {
		return Edit{Lines: l}
	}
	gone := []string{}
	for i := l.First - 1; i < l.Last; i++ { // line by line: two swapped versions both count
		kept := map[string]bool{}
		for _, w := range words(cur[i]) {
			kept[w] = true
		}
		for _, w := range words(old[i]) {
			if !kept[w] {
				gone = append(gone, w)
			}
		}
	}
	return Edit{l, gone}
}

// words are the names and versions in line, as "body-parser" and "2.3.0"; a
// run of punctuation, as "--" or "//", is none.
func words(line string) []string {
	return slices.DeleteFunc(strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-@/", r)
	}), func(w string) bool {
		return !strings.ContainsFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
	})
}

// Lines is a range of a file's lines, 1-based: the first and the last.
type Lines struct{ First, Last int }

// Covers reports whether lines first to last share a line with l.
func (l Lines) Covers(first, last int) bool { return first <= l.Last && last >= l.First }

// Replaced is the lines of before that after replaces: past the lines both
// start with and before those both end with. An insertion replaces none, so
// it is the two lines around it, which a citation spanning it covers. An edit
// that adds or removes lines moves every line after it, so those count too: a
// carried citation of them would point at other lines.
// ponytail: one range from the first difference to the last; diff into hunks
// if two edits far apart in one manifest rewrite pages citing neither, and
// shift carried citations past an edit if added dependencies rewrite many.
func Replaced(before, after string) Lines {
	old, cur := strings.Split(before, "\n"), strings.Split(after, "\n")
	head := 0
	for head < min(len(old), len(cur)) && old[head] == cur[head] {
		head++
	}
	if head == len(old) && head == len(cur) {
		return Lines{} // the same text, as after a change of mode only: no line
	}
	tail := 0
	for tail < min(len(old), len(cur))-head && old[len(old)-1-tail] == cur[len(cur)-1-tail] {
		tail++
	}
	first, last := head+1, len(old)-tail
	if first > last {
		first, last = head, head+1
	}
	if len(old) != len(cur) {
		last = max(last, len(old))
	}
	return Lines{first, last}
}

// All is every line of content: what removing the file replaces.
func All(content string) Lines { return Lines{1, strings.Count(content, "\n") + 1} }
