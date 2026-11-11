// Package python reads Python: its functions and classes as symbols, and the
// modules it imports as facts for the graph's Python resolver.
package python

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"

	"deepcode/internal/repo/index/syntax"
)

// Python is how .py files are read.
var Python = &syntax.Grammar{
	Language: sitter.NewLanguage(tspython.Language()),
	Decl:     decl,
	Facts:    facts,
	Refs:     syntax.Calls,
	// Doc comments and decorators are before the definition they describe.
	Leading: syntax.Kinds("comment"),
}

// decl reads the symbols that Python statement n declares: a function, a
// class and its methods, or a decorated definition of either.
func decl(x *syntax.Extractor, n *sitter.Node) []syntax.Symbol {
	switch n.Kind() {
	case "function_definition":
		return x.Named(n, n, "function", nil)
	case "class_definition":
		name := x.Text(n.ChildByFieldName("name"))
		body := x.Block(n.ChildByFieldName("body"))
		for i := range body {
			if body[i].Kind == "function" {
				body[i].Kind = "method"
			}
			body[i].Container = name
		}
		return x.Named(n, n, "class", body)
	case "decorated_definition":
		inner := n.ChildByFieldName("definition")
		out := decl(x, inner)
		for i := range out {
			out[i].StartLine = min(out[i].StartLine, int(n.StartPosition().Row)+1)
		}
		return out
	case "type_alias_statement":
		return x.Named(n, n, "type", nil)
	}
	return nil
}
