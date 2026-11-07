// Package analyzer detects file languages and extracts symbols and imports
// with Tree-sitter.
package analyzer

import (
	"sort"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type Symbol struct {
	Name      string
	Kind      string // function, method, class, interface, struct, type, enum, namespace
	Container string // receiver type for Go methods
	StartLine int    // 1-based, inclusive; widened to cover a leading doc comment
	EndLine   int
	Signature string
	Children  []Symbol
}

// QualifiedName is the name search shows: Receiver.Method for Go methods,
// Parent.Member inside classes and namespaces.
func (s Symbol) QualifiedName(parent string) string {
	switch {
	case s.Container != "":
		return s.Container + "." + s.Name
	case parent != "":
		return parent + "." + s.Name
	}
	return s.Name
}

var languages = map[string]*sitter.Language{
	"go":         sitter.NewLanguage(tsgo.Language()),
	"javascript": sitter.NewLanguage(tsjs.Language()),
	"typescript": sitter.NewLanguage(tsts.LanguageTypescript()),
	"tsx":        sitter.NewLanguage(tsts.LanguageTSX()),
}

// Parser holds one Tree-sitter parser per language. Not safe for concurrent use.
type Parser struct {
	parsers map[string]*sitter.Parser
}

func NewParser() *Parser { return &Parser{parsers: map[string]*sitter.Parser{}} }

func (p *Parser) Close() {
	for _, ps := range p.parsers {
		ps.Close()
	}
}

// File is what Parse finds in a source file: its top-level declarations (with
// class members and namespace contents as children) and the modules it
// imports, as its source spells them.
type File struct {
	Symbols []Symbol
	Imports []string
}

// HasGrammar reports whether Parse reads lang; it finds nothing in others.
func HasGrammar(lang string) bool { return languages[lang] != nil }

// Grammars lists the languages Parse reads, sorted.
func Grammars() []string {
	out := make([]string, 0, len(languages))
	for l := range languages {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Parse reads src as lang, in one pass for its symbols and its imports.
func (p *Parser) Parse(lang string, src []byte) File {
	ps := p.parsers[lang]
	if ps == nil {
		l := languages[lang]
		if l == nil {
			return File{}
		}
		ps = sitter.NewParser()
		if err := ps.SetLanguage(l); err != nil {
			ps.Close()
			return File{}
		}
		p.parsers[lang] = ps
	}
	tree := ps.Parse(src, nil)
	if tree == nil {
		return File{}
	}
	defer tree.Close()
	x := &extractor{src: src}
	return File{Symbols: x.block(tree.RootNode()), Imports: x.imports(tree.RootNode(), lang)}
}
