// Package jvm reads Java: its classes, interfaces, enums, records and
// annotation types with their members, and the facts the graph package's
// Java resolver turns into file edges.
package jvm

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"

	"deepcode/internal/repo/index/syntax"
)

// Java is the grammar for .java files. Javadoc is a block_comment; annotations
// sit inside a declaration's modifiers, so its range holds them already.
var Java = &syntax.Grammar{
	Language: sitter.NewLanguage(tsjava.Language()),
	Decl:     decl,
	Facts:    facts,
	Refs:     syntax.Calls,
	Leading:  syntax.Kinds("line_comment", "block_comment"),
}

// typeKinds maps the type declarations to their symbol kinds.
var typeKinds = map[string]string{
	"class_declaration":           "class",
	"interface_declaration":       "interface",
	"enum_declaration":            "enum",
	"record_declaration":          "record",
	"annotation_type_declaration": "annotation",
}

// decl reads the symbols that Java node n declares: a type with its members,
// a method, a constructor or a field.
func decl(x *syntax.Extractor, n *sitter.Node) []syntax.Symbol {
	switch kind := typeKinds[n.Kind()]; {
	case kind != "":
		return x.Named(n, n, kind, x.Block(n.ChildByFieldName("body")))
	case n.Kind() == "method_declaration", n.Kind() == "annotation_type_element_declaration":
		return x.Named(n, n, "method", nil)
	case n.Kind() == "constructor_declaration", n.Kind() == "compact_constructor_declaration":
		return x.Named(n, n, "constructor", nil)
	case n.Kind() == "enum_body_declarations": // an enum's members, after its constants
		return x.Block(n)
	}
	return nil
}
