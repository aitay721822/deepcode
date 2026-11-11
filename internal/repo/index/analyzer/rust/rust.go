// Package rust reads Rust: its items as symbols, and the modules and paths a
// file names as facts for the graph's Rust resolver.
package rust

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"

	"deepcode/internal/repo/index/syntax"
)

// Rust is how .rs files are read.
var Rust = &syntax.Grammar{
	Language: sitter.NewLanguage(tsrust.Language()),
	Decl:     decl,
	Facts:    facts,
	Refs:     syntax.Calls,
	// Doc comments and #[derive] are siblings before the item they describe.
	Leading: syntax.Kinds("line_comment", "block_comment", "attribute_item"),
}

// kinds are the items that become a symbol of their own, with no members.
var kinds = map[string]string{
	"function_item": "function", "function_signature_item": "function",
	"struct_item": "struct", "enum_item": "enum", "union_item": "union",
	"type_item": "type", "macro_definition": "macro",
}

// decl reads the symbols that Rust item n declares.
func decl(x *syntax.Extractor, n *sitter.Node) []syntax.Symbol {
	switch n.Kind() {
	case "trait_item":
		return x.Named(n, n, "trait", methods(x, n, ""))
	case "impl_item": // only its methods: one type has many impl blocks, as Go has many methods
		return methods(x, n, typeName(x, n.ChildByFieldName("type")))
	case "mod_item": // `mod a;` names a file, a fact rather than a symbol
		if body := n.ChildByFieldName("body"); body != nil {
			return x.Named(n, n, "namespace", x.Block(body))
		}
		return nil
	case "foreign_mod_item": // extern "C" { fn f(); }
		return x.Block(n.ChildByFieldName("body"))
	}
	if kind := kinds[n.Kind()]; kind != "" {
		return x.Named(n, n, kind, nil)
	}
	return nil
}

// methods are the functions of a trait or impl body, as methods of container;
// associated types and consts are left out.
func methods(x *syntax.Extractor, n *sitter.Node, container string) []syntax.Symbol {
	var out []syntax.Symbol
	for _, s := range x.Block(n.ChildByFieldName("body")) {
		if s.Kind == "function" {
			s.Kind, s.Container = "method", container
			out = append(out, s)
		}
	}
	return out
}

// typeName is the name of the type an impl block is for: Server for
// Server<S>, crate::a::Server or &Server, else the type as written:
// (StatusCode, R) names words search should find its methods by.
func typeName(x *syntax.Extractor, n *sitter.Node) string {
	for n != nil {
		switch n.Kind() {
		case "generic_type", "reference_type":
			n = n.ChildByFieldName("type")
		case "scoped_type_identifier":
			n = n.ChildByFieldName("name")
		default:
			return x.Text(n)
		}
	}
	return ""
}
