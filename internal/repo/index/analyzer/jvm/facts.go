package jvm

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"deepcode/internal/repo/index/syntax"
)

// facts lists what a Java file declares and uses, as the graph package's Java
// resolver reads them; declarations and imports first, since syntax keeps
// only a file's first facts:
//
//	package:a.b   the file's package
//	def:a.b.C     a top-level type it declares
//	import:a.b.C  a single-type import; a static import names its type
//	star:a.b      an on-demand import of a package's types (or a type's nested ones)
//	type:C        a type it names: C, Outer.Inner, or a.b.C spelled out
func facts(x *syntax.Extractor, root *sitter.Node) []string {
	var out []string
	pkg := ""
	for i := range root.NamedChildCount() {
		c := root.NamedChild(i)
		switch c.Kind() {
		case "package_declaration":
			if pkg = path(x, c); pkg != "" { // else broken: package a.;
				out = append(out, "package:"+pkg)
			}
		case "import_declaration":
			out = append(out, importFact(x, c))
		default:
			name := x.Text(c.ChildByFieldName("name"))
			if typeKinds[c.Kind()] == "" || name == "" {
				continue
			}
			if pkg != "" {
				name = pkg + "." + name
			}
			out = append(out, "def:"+name)
		}
	}
	params := generics(x, root, map[string]bool{})
	for _, f := range out { // a parameter named like a type the file imports or declares may mean either
		if _, name, ok := strings.Cut(f, ":"); ok && (strings.HasPrefix(f, "import:") || strings.HasPrefix(f, "def:")) {
			delete(params, name[strings.LastIndexByte(name, '.')+1:])
		}
	}
	return slices.DeleteFunc(refs(x, root, false, out), func(f string) bool {
		return strings.HasPrefix(f, "type:") && params[f[len("type:"):]]
	})
}

// generics adds to set the type parameters n declares, T in class Box<T>:
// names of no file.
func generics(x *syntax.Extractor, n *sitter.Node, set map[string]bool) map[string]bool {
	if n.Kind() == "type_parameter" {
		set[x.FirstOfKind(n, "type_identifier")] = true
	}
	for i := range n.NamedChildCount() {
		generics(x, n.NamedChild(i), set)
	}
	return set
}

// path is the dotted name a package or import declaration spells.
func path(x *syntax.Extractor, n *sitter.Node) string {
	for i := range n.NamedChildCount() {
		if c := n.NamedChild(i); (c.Kind() == "scoped_identifier" || c.Kind() == "identifier") && !c.HasError() { // not import a.;
			return x.Text(c)
		}
	}
	return ""
}

// importFact reads an import: import static a.b.C.m and import static a.b.C.*
// both use type a.b.C.
func importFact(x *syntax.Extractor, n *sitter.Node) string {
	p := path(x, n)
	static, star := false, false
	for i := range n.ChildCount() {
		switch n.Child(i).Kind() {
		case "static":
			static = true
		case "asterisk":
			star = true
		}
	}
	if static && !star {
		p = p[:max(strings.LastIndexByte(p, '.'), 0)]
	}
	switch {
	case p == "":
		return ""
	case star && !static:
		return "star:" + p
	}
	return "import:" + p
}

// refs adds the types n names: type identifiers, annotations, and the
// uppercase objects of static calls (Users.find), constants (Status.ACTIVE)
// and method references (Helper::run). A dotted type (Map.Entry, a.b.C) is one
// name, its parts none; chain is set inside one, till its type arguments.
func refs(x *syntax.Extractor, n *sitter.Node, chain bool, out []string) []string {
	switch n.Kind() {
	case "type_identifier":
		if t := x.Text(n); !chain && upper(t) { // lowercase: var, or a package
			out = append(out, "type:"+t)
		}
		return out
	case "scoped_type_identifier":
		if !chain {
			out = append(out, "type:"+typeName(x, n))
		}
		chain = true
	case "type_arguments":
		chain = false
	case "annotation", "marker_annotation": // @Test, @org.junit.Test
		if t := x.Text(n.ChildByFieldName("name")); upper(t[strings.LastIndexByte(t, '.')+1:]) {
			out = append(out, "type:"+t)
		}
	case "method_invocation", "field_access", "method_reference":
		o := n.ChildByFieldName("object")
		if n.Kind() == "method_reference" {
			o = n.NamedChild(0)
		}
		if o != nil && o.Kind() == "identifier" && upper(x.Text(o)) {
			out = append(out, "type:"+x.Text(o))
		}
	}
	for i := range n.NamedChildCount() {
		out = refs(x, n.NamedChild(i), chain, out)
	}
	return out
}

// typeName is a dotted type's name without its type arguments: Outer.Inner
// for Outer<String>.Inner.
func typeName(x *syntax.Extractor, n *sitter.Node) string {
	switch n.Kind() {
	case "scoped_type_identifier":
		return typeName(x, n.NamedChild(0)) + "." + x.Text(n.NamedChild(n.NamedChildCount()-1))
	case "generic_type":
		return typeName(x, n.NamedChild(0))
	}
	return x.Text(n)
}

// upper reports whether s starts with an uppercase letter, as Java type names do.
func upper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}
