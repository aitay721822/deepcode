// Package names reads the files and folders a text names: a question's, as a
// search reads them.
package names

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"deepcode/internal/repo/index/analyzer"
)

// lineRe matches the line a word names after its file: a.go:42, a.go#L42-L50.
var lineRe = regexp.MustCompile(`(?:(?::\d+)+(?:-\d+)?|#L\d+(?:-L?\d+)?)$`) // a.go:42:7 too

// FileNames are the first 16 words of query that may name files or folders,
// each once: a path (src/app.ts, examples/, app/[slug]), a glob (tests/*.rs)
// or the name of a file of an indexed language (App.java, go.mod; not
// res.send or .NET), never a URL. Quotes, full-width punctuation and stops
// around a word go, as do markdown's bold, brackets wrapping it or left
// unpaired, the line after it (a.go:42, a.go#L42) and a leading ./ or /. A
// file of an indexed language in backticks is one word, its spaces kept:
// `src/Events/OrderPaid .cs`.
func FileNames(query string) []string {
	var out []string
	for _, w := range words(query) {
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

// words are query's words as FileNames reads them, in order: split where
// separates says, except that the text between two backticks on one line is
// one word when it is a file of an indexed language, spaces and all.
func words(query string) []string {
	parts := strings.Split(query, "`")
	var out []string
	for i, p := range parts {
		inside := i%2 == 1 && i < len(parts)-1 && !strings.Contains(p, "\n")
		if _, indexed := analyzer.Detect(strings.TrimSpace(p)); inside && indexed && strings.Contains(strings.TrimSpace(p), " ") {
			out = append(out, strings.TrimSpace(p))
			continue
		}
		out = append(out, strings.FieldsFunc(p, separates)...)
	}
	return out
}

// separates reports whether r ends a word FileNames reads: a space, or
// punctuation no path holds, full-width too.
func separates(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(",;'\"|=<>，、；：。？！（）「」『』《》", r)
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
