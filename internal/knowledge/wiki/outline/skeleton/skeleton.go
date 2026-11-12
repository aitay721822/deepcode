// Package skeleton holds a wiki page's text to the headings code gives it:
// Build makes them from the page's plan (this file); the request lists them
// (prompt.go), and the Markdown draft of the page is checked, repaired and
// rendered against them (draft.go).
package skeleton

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/repo/index/analyzer"
)

// Heading is a heading a page's text is held to: its name as rendered, its
// level (2 or 3), the unit or file it heads ("" for a fixed one), and the
// line its text rests on (none for a fixed one).
type Heading struct {
	Name   string           `json:"name"`
	Level  int              `json:"level"`
	Unit   string           `json:"unit"`
	Anchor retrieval.Anchor `json:"anchor"`
}

// Skeleton is a page's headings, in order.
type Skeleton []Heading

// The most level-2 headings a page has, and level-3 ones under one.
const (
	maxHeads    = 6
	maxChildren = 4
)

// Source is what Build reads of the snapshot: the text of the guides and
// READMEs a page's units name (Texts), its chosen files' declarations, and
// each file's lines.
type Source struct {
	Texts map[string]string
	Decls map[string][]store.Decl
	Lines map[string]int
}

// Texts are the files whose text Build reads for units: their guides and
// the READMEs their recipes come from.
func Texts(units []string) []string {
	var out []string
	for _, u := range units {
		if k, file := kind(u); (k == analyzer.Docs || k == recipe) && !slices.Contains(out, file) {
			out = append(out, file)
		}
	}
	return out
}

// recipe is the kind of a README recipe's unit, readme + "#" + its title.
const recipe = "recipe"

// kind is the kind of unit u, and the file its text is in (a guide's or a
// recipe's README): a recipe, a folder of tests, an example (a folder, or a
// file directly in examples/), a guide (any other file: its units are
// folders, "./" and "tooling" aside), or "" for one with no heading.
func kind(u string) (string, string) {
	if readme, _, ok := strings.Cut(u, "#"); ok && !strings.Contains(readme, "/") {
		return recipe, readme
	}
	switch {
	case strings.HasSuffix(u, place.TestsMark):
		return analyzer.Tests, ""
	case analyzer.Aside(u) == analyzer.Examples:
		return analyzer.Examples, ""
	case !strings.HasSuffix(u, "/") && u != analyzer.Tooling:
		return analyzer.Docs, u
	}
	return "", ""
}

// Build is the skeleton of a page covering units, its chosen files chosen
// and all its files files, in order: a heading for each unit that has one (a
// guide by its title (outline.Title), else its name, with a level-3 heading
// for each of up to maxChildren of its sections that show code; an example by
// its folder's name; a folder of tests by its path's last two parts, the
// root's "tests"), then one for each README recipe, then each chosen file's
// (subjects). A name one file gives twice (a guide's section named as the
// guide) is headed once. It keeps maxHeads level-2 headings, each with its
// own; the chosen files' are the first left out. Of those, it keeps at most
// most headings with an anchor, as page search takes no more: every level-2
// one, then the level-3 ones in order. Headings of one name from different
// files each add their path.
func Build(units, chosen, files []string, src Source, most int) Skeleton {
	var asides, recipes, out Skeleton
	for _, u := range units {
		k, file := kind(u)
		switch k {
		case analyzer.Docs:
			title, at := outline.Title(src.Texts[file])
			asides = append(asides, Heading{Name: cmp.Or(title, bare(file)), Level: 2, Unit: u, Anchor: retrieval.Anchor{Path: file, Line: at}})
			shown := outline.Shown(src.Texts[file])
			for _, s := range shown[:min(maxChildren, len(shown))] {
				asides = append(asides, Heading{Name: s.Title, Level: 3, Unit: u, Anchor: retrieval.Anchor{Path: file, Line: s.Line}})
			}
		case analyzer.Examples:
			if strings.HasSuffix(u, "/") {
				asides = append(asides, Heading{Name: path.Base(u), Level: 2, Unit: u, Anchor: largest(files, src.Lines, func(f string) bool { return strings.HasPrefix(f, u) })})
			} else {
				asides = append(asides, Heading{Name: bare(u), Level: 2, Unit: u, Anchor: retrieval.Anchor{Path: u}})
			}
		case analyzer.Tests:
			dir := strings.TrimSuffix(strings.TrimSuffix(u, place.TestsMark), "/")
			parts := strings.Split(dir, "/")
			name := strings.Join(parts[max(0, len(parts)-2):], "/")
			if dir == "." {
				name = analyzer.Tests
			}
			asides = append(asides, Heading{Name: name, Level: 2, Unit: u,
				Anchor: largest(files, src.Lines, func(f string) bool { return path.Dir(f) == dir })})
		case recipe:
			_, title, _ := strings.Cut(u, "#")
			h := Heading{Name: title, Level: 2, Unit: u}
			shown := outline.Shown(src.Texts[file])
			if i := slices.IndexFunc(shown, func(s outline.Section) bool { return s.Title == title }); i >= 0 {
				h.Anchor = retrieval.Anchor{Path: file, Line: shown[i].Line}
			}
			recipes = append(recipes, h)
		}
	}
	out = append(asides, recipes...)
	for _, f := range chosen {
		out = append(out, subjects(f, src.Decls[f])...)
	}
	seen := map[[2]string]bool{}
	out = slices.DeleteFunc(out, func(h Heading) bool {
		k := [2]string{fold(h.Name), cmp.Or(h.Anchor.Path, h.Unit)}
		dup := seen[k]
		seen[k] = true
		return dup
	})
	heads := 0
	out = slices.DeleteFunc(out, func(h Heading) bool {
		if h.Level == 2 {
			heads++
		}
		return heads > maxHeads
	})
	room := most // what the level-2 headings' anchors leave the level-3 ones, which all have one
	for _, h := range out {
		if h.Level == 2 && h.Anchor.Path != "" {
			room--
		}
	}
	out = slices.DeleteFunc(out, func(h Heading) bool {
		if h.Level == 3 {
			room--
		}
		return room < 0 && h.Level == 3
	})
	return unique(out)
}

// Others are the paths no heading of s covers, files first, each once: of
// files, one no heading is of, nor of its guide or README, nor of the guide
// that shows it (home, the docs aside's Home), nor of an example folder
// holding it, nor, for a test, of a tests folder holding it (code beside the
// tests is not theirs); of units, the file or folder of one whose kind has a
// heading (a guide, recipe, example or tests unit) when s gives none.
func Others(s Skeleton, files, units []string, home map[string]string) []string {
	covered := func(p string) bool {
		return slices.ContainsFunc(s, func(h Heading) bool {
			_, file := kind(h.Unit)
			dir, tests := strings.CutSuffix(h.Unit, place.TestsMark)
			in := strings.HasSuffix(dir, "/") && strings.HasPrefix(p, dir) || dir == "./" && !strings.Contains(p, "/")
			return h.Unit != "" && (h.Unit == p || file == p || file != "" && home[p] == file || in && (!tests || analyzer.Aside(p) == analyzer.Tests))
		})
	}
	var out []string
	add := func(p string) {
		if !covered(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, f := range files {
		add(f)
	}
	for _, u := range units {
		if k, file := kind(u); k != "" {
			add(cmp.Or(file, strings.TrimSuffix(u, place.TestsMark)))
		}
	}
	return out
}

// subjects are the headings of chosen file f: its weightiest type or function
// at level 2, then up to maxChildren more at level 3, of that type's methods
// and the file's other types and functions, weightiest first, each name once.
// A type weighs the lines of its span and its methods' (a C# class holds its
// methods, a Rust struct's are in impl blocks); a function or a method, its
// span's. Ties go to a type, then to the first. Inline tests (a declaration
// in a tests or test module, Rust's mod tests) are no subject; a function or
// method named test is. A file with no type or function is headed by its
// name at its first chunk (0): C#'s top-level statements declare only a
// synthetic Main method, so Program.cs is headed "Program".
func subjects(f string, decls []store.Decl) []Heading {
	type subject struct {
		d      store.Decl
		weight int
		typ    bool
	}
	rank := func(s []subject) {
		slices.SortStableFunc(s, func(a, b subject) int {
			switch {
			case a.weight != b.weight:
				return b.weight - a.weight
			case a.typ && !b.typ:
				return -1
			case b.typ && !a.typ:
				return 1
			}
			return a.d.Line - b.d.Line
		})
	}
	var tops []subject
	methods := map[string][]store.Decl{} // by their type's qualified name
	for _, d := range decls {
		if q := strings.Split(d.Qualified, "."); slices.ContainsFunc(q[:len(q)-1], func(p string) bool { return strings.EqualFold(p, "tests") || strings.EqualFold(p, "test") }) {
			continue
		}
		switch d.Kind {
		case "method":
			if i := strings.LastIndex(d.Qualified, "."); i > 0 {
				methods[d.Qualified[:i]] = append(methods[d.Qualified[:i]], d)
			}
		case "function", "macro":
			tops = append(tops, subject{d: d, weight: max(1, d.End-d.Line+1)})
		default:
			tops = append(tops, subject{d: d, typ: true})
		}
	}
	key := func(d store.Decl) string { return cmp.Or(d.Qualified, d.Name) }
	for i, s := range tops {
		if !s.typ {
			continue
		}
		lines := map[int]bool{}
		for _, d := range append([]store.Decl{s.d}, methods[key(s.d)]...) {
			for l := d.Line; l <= max(d.Line, d.End); l++ {
				lines[l] = true
			}
		}
		tops[i].weight = len(lines)
	}
	if rank(tops); len(tops) == 0 {
		return []Heading{{Name: bare(f), Level: 2, Unit: f, Anchor: retrieval.Anchor{Path: f}}}
	}
	top := tops[0]
	out := []Heading{{Name: top.d.Name, Level: 2, Unit: f, Anchor: retrieval.Anchor{Path: f, Line: top.d.Line}}}
	pool := slices.Clone(tops[1:])
	if top.typ {
		for _, m := range methods[key(top.d)] {
			pool = append(pool, subject{d: m, weight: max(1, m.End-m.Line+1)})
		}
	}
	rank(pool)
	for _, s := range pool {
		if len(out) > maxChildren {
			break
		}
		if !slices.ContainsFunc(out, func(h Heading) bool { return fold(h.Name) == fold(s.d.Name) }) {
			out = append(out, Heading{Name: s.d.Name, Level: 3, Unit: f, Anchor: retrieval.Anchor{Path: f, Line: s.d.Line}})
		}
	}
	return out
}

// bare is the name of file f without its extension.
func bare(f string) string {
	b := path.Base(f)
	return strings.TrimSuffix(b, path.Ext(b))
}

// largest is the anchor of the largest of files that in holds, at its first
// chunk; none when it holds none.
func largest(files []string, lines map[string]int, in func(string) bool) retrieval.Anchor {
	best := ""
	for _, f := range files {
		if in(f) && (best == "" || lines[f] > lines[best]) {
			best = f
		}
	}
	return retrieval.Anchor{Path: best}
}

// unique adds the path of its file or unit to each heading whose name
// another one shares, case aside.
func unique(s Skeleton) Skeleton {
	seen := map[string]int{}
	for _, h := range s {
		seen[fold(h.Name)]++
	}
	for i, h := range s {
		if seen[fold(h.Name)] > 1 {
			s[i].Name += " (" + cmp.Or(h.Anchor.Path, h.Unit) + ")"
		}
	}
	return s
}

// fold is a heading's name as headings match: case, spaces at its ends, and
// the backticks and asterisks Markdown wraps names in aside.
func fold(name string) string {
	return strings.ToLower(strings.TrimSpace(strings.NewReplacer("`", "", "*", "").Replace(name)))
}
