// Package analyzer detects file languages and extracts symbols and imports
// with Tree-sitter: Go, JavaScript and TypeScript here, other languages in a
// folder each, all read through the syntax package.
package analyzer

import (
	"sort"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"deepcode/internal/repo/index/analyzer/dotnet"
	"deepcode/internal/repo/index/analyzer/jvm"
	"deepcode/internal/repo/index/analyzer/rust"
	"deepcode/internal/repo/index/analyzer/syntax"
)

type Symbol = syntax.Symbol

var comments = syntax.Kinds("comment")

var languages = map[string]*syntax.Grammar{
	"go":         {Language: sitter.NewLanguage(tsgo.Language()), Decl: decl, Facts: goImports, Leading: comments},
	"javascript": {Language: sitter.NewLanguage(tsjs.Language()), Decl: decl, Facts: jsImports, Leading: comments},
	"typescript": {Language: sitter.NewLanguage(tsts.LanguageTypescript()), Decl: decl, Facts: jsImports, Leading: comments},
	"tsx":        {Language: sitter.NewLanguage(tsts.LanguageTSX()), Decl: decl, Facts: jsImports, Leading: comments},
	"csharp":     dotnet.CSharp,
	"java":       jvm.Java,
	"rust":       rust.Rust,
}

// Parser holds one Tree-sitter parser per language. Not safe for concurrent use.
type Parser struct {
	parsers map[string]*sitter.Parser
}

// NewParser returns a Parser with no grammar loaded yet.
func NewParser() *Parser { return &Parser{parsers: map[string]*sitter.Parser{}} }

// Close frees the parsers p made.
func (p *Parser) Close() {
	for _, ps := range p.parsers {
		ps.Close()
	}
}

// File is what Parse finds in a source file: its top-level declarations (with
// class members and namespace contents as children) and its imports: for Go,
// JavaScript and TypeScript the modules as its source spells them, for other
// languages the facts their resolver in the graph package reads.
type File struct {
	Symbols []Symbol
	Imports []string
}

// Version is raised whenever what Detect, Generated or Parse make of a file
// changes: a snapshot an older version indexed lends the next none of its
// files' rows, so each is read again. 1 added C#, Java and Rust; 2, JMeter
// test plans.
const Version = 2

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
	g := languages[lang]
	if g == nil {
		return File{}
	}
	ps := p.parsers[lang]
	if ps == nil {
		ps = sitter.NewParser()
		if err := ps.SetLanguage(g.Language); err != nil {
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
	syms, imports := syntax.Extract(g, tree.RootNode(), src)
	return File{Symbols: syms, Imports: imports}
}
