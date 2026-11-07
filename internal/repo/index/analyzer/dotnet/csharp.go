// Package dotnet reads C# with Tree-sitter: its types and their members as
// symbols, and as facts its namespaces, usings, the types it declares and the
// types it refers to, which the graph's dotnet resolver turns into edges.
package dotnet

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	tscs "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"

	"deepcode/internal/repo/index/analyzer/syntax"
)

// CSharp is the C# grammar. A file-scoped namespace (namespace A;) is a
// sibling of the declarations it holds, so it is a Scope.
var CSharp = &syntax.Grammar{
	Language: sitter.NewLanguage(tscs.Language()),
	Decl:     decl,
	Facts:    facts,
	Leading:  syntax.Kinds("comment"),
	Scope:    "file_scoped_namespace_declaration",
}

// types maps a type declaration's node kind to its symbol kind; record struct
// parses as a record_declaration.
var types = map[string]string{
	"class_declaration": "class", "struct_declaration": "struct", "record_declaration": "record",
	"interface_declaration": "interface", "enum_declaration": "enum", "delegate_declaration": "delegate",
}

// decl turns a C# declaration into symbols: a type with its members as
// children, a namespace with its declarations. Attributes sit inside the
// declaration's node, so its span covers them.
func decl(x *syntax.Extractor, n *sitter.Node) []syntax.Symbol {
	if kind := types[n.Kind()]; kind != "" {
		return x.Named(n, n, kind, x.Block(n.ChildByFieldName("body")))
	}
	switch n.Kind() {
	case "namespace_declaration":
		body := x.Block(n.ChildByFieldName("body"))
		if ns := x.Named(n, n, "namespace", body); ns != nil {
			return ns
		}
		return body // namespace { }: the name is broken, not what it holds
	case "file_scoped_namespace_declaration": // Block gives it the declarations after it
		return x.Named(n, n, "namespace", nil)
	case "method_declaration":
		return x.Named(n, n, "method", nil)
	case "constructor_declaration":
		return x.Named(n, n, "constructor", nil)
	case "destructor_declaration":
		name := n.ChildByFieldName("name")
		return named(x, "~"+x.Text(name), "method", n, name)
	case "property_declaration":
		return x.Named(n, n, "property", nil)
	case "indexer_declaration": // it has no name of its own
		return named(x, "this", "indexer", n, n.ChildByFieldName("parameters"))
	case "event_declaration": // event EventHandler E { add {} remove {} }
		return x.Named(n, n, "event", nil)
	case "event_field_declaration": // event EventHandler A, B;
		return events(x, n)
	case "operator_declaration":
		op := n.ChildByFieldName("operator")
		return named(x, "operator "+x.Text(op), "operator", n, op)
	case "conversion_operator_declaration": // implicit operator int(...)
		t := n.ChildByFieldName("type")
		return named(x, "operator "+x.Text(t), "operator", n, t)
	case "preproc_if", "preproc_elif", "preproc_else": // declarations under #if DEBUG
		return x.Block(n)
	}
	return nil
}

// named is a symbol called name spanning n, its signature the line holding at
// (past n's attributes), none when the name's node is missing from a broken
// parse ("~", "operator ").
func named(x *syntax.Extractor, name, kind string, n, at *sitter.Node) []syntax.Symbol {
	if name == "~" || name == "operator " {
		return nil
	}
	s := x.Make(name, kind, n, nil)
	if at != nil {
		s.Signature = x.LineOf(n, at)
	}
	return []syntax.Symbol{s}
}

// events is one symbol per declarator of an event field: the whole
// declaration for a lone one, so its attributes count.
func events(x *syntax.Extractor, n *sitter.Node) []syntax.Symbol {
	var decls []*sitter.Node
	for i := range n.NamedChildCount() {
		v := n.NamedChild(i)
		for j := uint(0); v.Kind() == "variable_declaration" && j < v.NamedChildCount(); j++ {
			if d := v.NamedChild(j); d.Kind() == "variable_declarator" {
				decls = append(decls, d)
			}
		}
	}
	var out []syntax.Symbol
	for _, d := range decls {
		span := d
		if len(decls) == 1 {
			span = n
		}
		at := d // public event Action A, B;
		if v := d.Parent(); v != nil && v.StartPosition().Row != d.StartPosition().Row {
			at = v // a declarator on a line of its own: the line naming the type
		}
		for _, s := range x.Named(d, span, "event", nil) {
			s.Signature = x.LineOf(n, at)
			out = append(out, s)
		}
	}
	return out
}
