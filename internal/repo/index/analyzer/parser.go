// Package analyzer detects file languages and extracts symbols with Tree-sitter.
package analyzer

import (
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

// Symbols returns the top-level declarations of src (with class members and
// namespace contents as children), or nil for languages without a grammar.
func (p *Parser) Symbols(lang string, src []byte) []Symbol {
	ps := p.parsers[lang]
	if ps == nil {
		l := languages[lang]
		if l == nil {
			return nil
		}
		ps = sitter.NewParser()
		if err := ps.SetLanguage(l); err != nil {
			ps.Close()
			return nil
		}
		p.parsers[lang] = ps
	}
	tree := ps.Parse(src, nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()
	return (&extractor{src: src}).block(tree.RootNode())
}
