// Package jvm turns Java files' facts, as the analyzer's jvm grammar lists
// them, into file edges: a file uses the files declaring the types it imports
// and names.
package jvm

import (
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxEdges bounds the files one file uses.
// ponytail: the first 50 files by import, then by use; a file past it is a
// hub whose edges say little. Raise it if large files lose edges that matter.
const maxEdges = 50

// Resolve returns the edges {src, dst}: src uses a type dst declares. Types
// resolve by the packages files declare, not by their paths, so configs is
// unused.
func Resolve(files map[string][]string, configs map[string]string) [][2]string {
	defs := map[string][]string{} // a.b.C → the files declaring it
	paths := slices.Sorted(maps.Keys(files))
	for _, p := range paths {
		for _, f := range files[p] {
			if fqn, ok := strings.CutPrefix(f, "def:"); ok {
				defs[fqn] = append(defs[fqn], p)
			}
		}
	}
	var out [][2]string
	for _, p := range paths {
		out = append(out, edges(p, files[p], defs)...)
	}
	return out
}

// edges are the files src uses. Its facts list the package and imports before
// any type, so each type resolves as Java finds it: a single-type import, then
// src's own package, then its on-demand imports. A dotted name starting
// lowercase (a.b.C) is spelled out in full.
func edges(src string, facts []string, defs map[string][]string) [][2]string {
	var out [][2]string
	seen := map[string]bool{src: true}
	add := func(dsts []string) {
		for _, d := range dsts {
			if !seen[d] && len(out) < maxEdges {
				seen[d] = true
				out = append(out, [2]string{src, d})
			}
		}
	}
	pkg, stars, imported := "", []string(nil), map[string]bool{}
	for _, f := range facts {
		kind, v, _ := strings.Cut(f, ":")
		switch kind {
		case "package":
			pkg = v + "."
		case "import":
			imported[v[strings.LastIndexByte(v, '.')+1:]] = true
			add(owner(v, defs))
		case "star":
			stars = append(stars, v+".")
		case "type":
			first, _, _ := strings.Cut(v, ".")
			switch {
			case !upper(first):
				add(owner(v, defs))
			case imported[first]: // its import has the edge
			default:
				if d := owner(pkg+v, defs); d != nil {
					add(d)
					continue
				}
				for _, s := range stars {
					add(owner(s+v, defs))
				}
			}
		}
	}
	return out
}

// owner is the files declaring the longest prefix of fqn that is a type:
// a.b.Outer.Inner and a.b.C.member belong to a.b.Outer's and a.b.C's.
func owner(fqn string, defs map[string][]string) []string {
	for {
		if d := defs[fqn]; d != nil {
			return d
		}
		i := strings.LastIndexByte(fqn, '.')
		if i < 0 {
			return nil
		}
		fqn = fqn[:i]
	}
}

// upper reports whether s starts with an uppercase letter, as type names do
// and package names do not.
func upper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}
