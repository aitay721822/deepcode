package rust

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"deepcode/internal/repo/index/syntax"
)

// facts lists what a Rust file names, in the order the graph's resolver reads
// them best: "mod:name" for each `mod name;` (mod:outer/name inside an inline
// mod outer), then "use:a::b::C" for each path its use declarations import,
// lists expanded and aliases dropped (a::* as a), then the same for each path
// its code spells, as crate::util::helper() or routing::get(), in macro
// arguments too. A path reads from the file's module: inside an inline module
// its self:: and super:: are rewritten to say so. Paths into std, core and
// alloc, those through a name a use brings from them (fmt::Display after use
// std::fmt), and those rooted at a type (Self::new, Vec::new), are left out.
func facts(x *syntax.Extractor, root *sitter.Node) []string {
	r := &reader{x: x, std: map[string]bool{}}
	r.walk(root, nil)
	return slices.DeleteFunc(slices.Concat(r.mods, r.uses, r.paths), func(f string) bool {
		first, _, _ := strings.Cut(strings.TrimPrefix(f, "use:"), "::")
		return strings.HasPrefix(f, "use:") && r.std[first]
	})
}

// ponytail: std holds the names for the whole file, so an inline module's own
// use crate::fmt loses its fmt:: paths after a file-level use std::fmt; key
// it by module if that shows up.
type reader struct {
	x                 *syntax.Extractor
	mods, uses, paths []string
	std               map[string]bool // the names use declarations bring from std, core and alloc
}

// walk reads n and its descendants, inside the inline modules named inline.
func (r *reader) walk(n *sitter.Node, inline []string) {
	switch n.Kind() {
	case "mod_item":
		in := append(slices.Clip(inline), r.x.Text(n.ChildByFieldName("name")))
		if body := n.ChildByFieldName("body"); body != nil {
			r.walk(body, in)
		} else {
			r.mods = append(r.mods, "mod:"+strings.Join(in, "/"))
		}
		return
	case "use_declaration":
		r.use(n.ChildByFieldName("argument"), nil, inline)
		return
	case "scoped_identifier", "scoped_type_identifier":
		if segs := r.segments(n); segs != nil {
			r.paths = add(r.paths, segs, inline)
			return
		} // else a path through a type, as Vec::<crate::a::B>::new: read inside it
	case "token_tree":
		r.tokens(n, inline)
		return
	}
	for i := range n.NamedChildCount() {
		r.walk(n.NamedChild(i), inline)
	}
}

// use adds the paths a use tree imports, each below prefix.
func (r *reader) use(n *sitter.Node, prefix, inline []string) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "use_as_clause":
		if segs := slices.Concat(prefix, r.segments(n.ChildByFieldName("path"))); len(segs) > 0 && builtin(segs[0]) {
			r.std[r.x.Text(n.ChildByFieldName("alias"))] = true // use std::fmt as f
		}
		r.use(n.ChildByFieldName("path"), prefix, inline)
	case "use_wildcard": // use std::collections::* names nothing a path starts from
		if segs := slices.Concat(prefix, r.segments(n.NamedChild(0))); len(segs) > 0 && builtin(segs[0]) {
			return
		}
		r.use(n.NamedChild(0), prefix, inline)
	case "scoped_use_list":
		if p := n.ChildByFieldName("path"); p != nil {
			segs := r.segments(p)
			if segs == nil {
				return
			}
			prefix = slices.Concat(prefix, segs)
		}
		r.use(n.ChildByFieldName("list"), prefix, inline)
	case "use_list":
		for i := range n.NamedChildCount() {
			r.use(n.NamedChild(i), prefix, inline)
		}
	default:
		segs := r.segments(n)
		if segs == nil {
			return
		}
		segs = slices.Concat(prefix, segs)
		if len(segs) > 1 && segs[len(segs)-1] == "self" { // use std::fmt::{self}
			segs = segs[:len(segs)-1]
		}
		if builtin(segs[0]) {
			r.std[segs[len(segs)-1]] = true
			return
		}
		r.uses = add(r.uses, segs, inline)
	}
}

// builtin reports the crates every Rust program has, whose paths name no
// file of the repository.
func builtin(crate string) bool { return crate == "std" || crate == "core" || crate == "alloc" }

// tokens reads the paths in a macro's arguments and a macro_rules! body, which
// the grammar leaves as tokens: crate::a::f is five of them. $crate is crate;
// a path through another $var, or after `>::`, is left out.
func (r *reader) tokens(n *sitter.Node, inline []string) {
	var segs []string
	joined, lost := false, false
	flush := func() {
		if len(segs) > 1 && !lost {
			if segs[0] == "$crate" {
				segs[0] = "crate"
			}
			if !slices.ContainsFunc(segs, func(s string) bool { return strings.HasPrefix(s, "$") }) {
				r.paths = add(r.paths, segs, inline)
			}
		}
		segs, lost = nil, false
	}
	for i := range n.ChildCount() {
		c := n.Child(i)
		switch c.Kind() {
		case "::":
			lost = lost || len(segs) == 0 // $t::a, <T as Tr>::a
			joined = true
			continue
		case "identifier", "crate", "self", "super", "metavariable":
			if !joined {
				flush()
			}
			segs = append(segs, r.x.Text(c))
		case "token_tree":
			flush()
			r.tokens(c, inline)
		default:
			flush()
		}
		joined = false
	}
	flush()
}

// segments are the names of a plain path, crate::a::B; nil for one through a
// type (Vec::<u8>::new, <T as Tr>::f) or a macro's $var.
func (r *reader) segments(n *sitter.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Kind() {
	case "identifier", "type_identifier", "crate", "self", "super":
		return []string{r.x.Text(n)}
	case "scoped_identifier", "scoped_type_identifier":
		name := n.ChildByFieldName("name")
		if name == nil {
			return nil
		}
		var head []string
		if p := n.ChildByFieldName("path"); p != nil { // none in ::serde
			if head = r.segments(p); head == nil {
				return nil
			}
		}
		return append(head, r.x.Text(name))
	}
	return nil
}

// add appends a path to list as a fact, read from the file's module, unless
// it can name no file of the repository.
func add(list, segs, inline []string) []string {
	if len(segs) > 1 && segs[len(segs)-1] == "self" { // use a::{self}
		segs = segs[:len(segs)-1]
	}
	switch first := segs[0]; {
	case builtin(first), first == "", 'A' <= first[0] && first[0] <= 'Z':
		return list
	}
	return append(list, "use:"+strings.Join(fromFile(segs, inline), "::"))
}

// fromFile rewrites a path read inside the inline modules inline to read from
// the file's module: super:: in mod tests is self::.
func fromFile(segs, inline []string) []string {
	d := len(inline)
	if d == 0 {
		return segs
	}
	switch segs[0] {
	case "self":
		return slices.Concat([]string{"self"}, inline, segs[1:])
	case "super":
		k := 0
		for k < len(segs) && segs[k] == "super" {
			k++
		}
		if k <= d {
			return slices.Concat([]string{"self"}, inline[:d-k], segs[k:])
		}
		return slices.Concat(segs[:k-d], segs[k:])
	}
	return segs
}
