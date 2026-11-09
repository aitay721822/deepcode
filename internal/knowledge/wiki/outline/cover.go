package outline

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/repo/index/analyzer"
)

// Code reports whether f is code a page explains: a file of a language with
// a grammar, which declares what it holds, not a build or configuration file.
func Code(f string) bool {
	lang, ok := analyzer.Detect(f)
	return ok && analyzer.HasGrammar(lang) && !analyzer.Config(f)
}

// folderFiles is the most files a folder a query names may hold, of files,
// for it to name each of them: a page search reads a few chunks of the files
// its query names, which a larger folder would spread too thin to say what
// the page is about.
const folderFiles = 8

// Unnamed are those of files that are Code and that no page names in its
// title or query: by the file's path or name, by a folder holding it and at
// most folderFiles others, or by its name without the extension as a whole
// word (profiler.go by Profiler) when that is three letters or more, case
// aside. A page naming one file of a folder does not name the folder.
func Unnamed(pages []Page, files []string) []string {
	words, text := map[string]bool{}, ""
	for _, p := range pages {
		s := strings.ToLower(p.Title + " " + p.Query)
		text += s + "\n"
		for w := range strings.FieldsFuncSeq(s, separates) {
			words[strings.Trim(strings.TrimPrefix(w, "./"), "/.:")] = true
		}
	}
	held := holds(files)
	var out []string
	for _, f := range files {
		if !Code(f) {
			continue
		}
		lf := strings.ToLower(f)
		stem := strings.TrimSuffix(path.Base(lf), path.Ext(lf))
		hit := words[lf] || words[path.Base(lf)] || len(stem) >= 3 && named(text, stem)
		for d := path.Dir(lf); !hit && d != "."; d = path.Dir(d) {
			hit = words[d] && held[d] <= folderFiles
		}
		if !hit {
			out = append(out, f)
		}
	}
	return out
}

// holds counts the files under each folder of files, lowercased.
func holds(files []string) map[string]int {
	n := map[string]int{}
	for _, f := range files {
		for d := path.Dir(strings.ToLower(f)); d != "."; d = path.Dir(d) {
			n[d]++
		}
	}
	return n
}

// separates ends a word of a page's query: a path holds no space or comma.
func separates(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(",;`'\"|()[]{}<>", r)
}

// Cover names in pages' queries the code files of files that none names
// (Unnamed), so that each is some page's to explain: their folder for two or
// more of one folder holding at most folderFiles, else each file, the largest
// first, each at the end of the query of the page that names a path sharing
// the most folders with it, of those with room for it, then the page given
// fewest. One two folders deep or more goes only to a page naming a path in
// its first two (its project: src/Ordering.API/), as another project's page
// would read it beside its own subject. Room is within MaxQuery and among the
// names the page search reads (retrieval.FileNames): a 17th path would name a
// file no search reads. It returns the files left unnamed for want of room,
// which the caller reports: none is dropped unsaid.
func Cover(pages []Page, files []string, lines map[string]int) ([]Page, []string) {
	byDir := map[string][]string{}
	for _, f := range Unnamed(pages, files) {
		byDir[path.Dir(f)] = append(byDir[path.Dir(f)], f)
	}
	type unit struct {
		name  string
		files []string
		size  int
	}
	held := holds(files)
	var units []unit
	for d, fs := range byDir {
		if len(fs) >= 2 && d != "." && held[strings.ToLower(d)] <= folderFiles {
			units = append(units, unit{name: d + "/", files: fs})
		} else {
			for _, f := range fs {
				units = append(units, unit{name: f, files: []string{f}})
			}
		}
	}
	for i := range units {
		for _, f := range units[i].files {
			units[i].size += lines[f]
		}
	}
	slices.SortFunc(units, func(a, b unit) int { return cmp.Or(b.size-a.size, strings.Compare(a.name, b.name)) })
	given := make([]int, len(pages))
	var left []string
	for _, u := range units {
		best, most := -1, -1
		deep := len(folders(strings.ToLower(u.name))) >= 2
		for i, p := range pages {
			if q := strings.TrimSpace(p.Query + " " + u.name); utf8.RuneCountInString(q) > MaxQuery || !slices.Contains(retrieval.FileNames(q), u.name) {
				continue
			}
			n := shared(p.Title+" "+p.Query, u.name)
			if deep && n < 2 { // another project's page
				continue
			}
			if n > most || n == most && given[i] < given[best] {
				best, most = i, n
			}
		}
		if best < 0 {
			left = append(left, u.files...)
			continue
		}
		pages[best].Query = strings.TrimSpace(pages[best].Query + " " + u.name)
		given[best]++
	}
	slices.Sort(left)
	return pages, left
}

// shared is the most leading folders that a path text names has in common
// with the folder of unit (a file, or a folder ending in /): 2 for src/a/x.go
// and src/a/b/. A path's last part counts only when a / ends it, as a file's
// name is no folder.
func shared(text, unit string) int {
	dir := folders(strings.ToLower(unit))
	most := 0
	for w := range strings.FieldsFuncSeq(strings.ToLower(text), separates) {
		parts := folders(strings.TrimPrefix(w, "./"))
		n := 0
		for n < len(parts) && n < len(dir) && parts[n] == dir[n] {
			n++
		}
		most = max(most, n)
	}
	return most
}

// folders are the folders path p names: all its parts when a / ends it, else
// all but the last.
func folders(p string) []string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	return parts[:len(parts)-1]
}
