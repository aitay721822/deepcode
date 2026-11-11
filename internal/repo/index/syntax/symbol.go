// Package syntax is what every language's extraction shares: the symbols a
// file declares, a language's grammar and rules, and a walk over a
// Tree-sitter tree that attaches each declaration's leading comments.
package syntax

import sitter "github.com/tree-sitter/go-tree-sitter"

type Symbol struct {
	Name string
	// function, method, constructor, property, indexer, event, operator,
	// class, record, struct, union, interface, trait, enum, delegate,
	// annotation, type, macro, namespace
	Kind      string
	Container string // the receiver's type: Go methods, and Rust's in an impl block
	StartLine int    // 1-based, inclusive; widened to cover a leading doc comment or attribute
	EndLine   int
	Signature string
	Children  []Symbol
}

// QualifiedName is the name search shows: Receiver.Method for Go and Rust
// methods, Parent.Member inside classes and namespaces.
func (s Symbol) QualifiedName(parent string) string {
	switch {
	case s.Container != "":
		return s.Container + "." + s.Name
	case parent != "":
		return parent + "." + s.Name
	}
	return s.Name
}

// Reference is a name a file calls, at the line it calls it: where a
// name-based call graph tries an edge, when the name is declared once.
type Reference struct {
	Name string
	Line int
}

// Grammar is how one language is read.
type Grammar struct {
	Language *sitter.Language
	// Decl turns a declaration into symbols, nil for any other node. It reads
	// a body's members with Extractor.Block.
	Decl func(x *Extractor, n *sitter.Node) []Symbol
	// Facts lists what the file imports or refers to, in the form its
	// language's resolver in the graph package reads; nil reads none.
	Facts func(x *Extractor, root *sitter.Node) []string
	// Refs lists the names its calls name, at their lines; nil reads none.
	Refs func(x *Extractor, root *sitter.Node) []Reference
	// Leading are the node kinds that belong to the declaration after them:
	// comments, and attributes where they are siblings (Rust's #[derive]).
	Leading map[string]bool
	// Scope is a node kind whose following siblings are its body: C#'s
	// file-scoped namespace. Decl turns it into a symbol, which takes them as
	// its children.
	Scope string
}

// Kinds is a set of node kinds, for Grammar.Leading.
func Kinds(kinds ...string) map[string]bool {
	m := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		m[k] = true
	}
	return m
}
