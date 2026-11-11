// Package analyzer detects file languages and extracts symbols and imports
// with Tree-sitter: Go, JavaScript and TypeScript here, other languages in a
// folder each, all read through the syntax package.
package analyzer

import (
	"sort"
	"strconv"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"deepcode/internal/repo/index/analyzer/dotnet"
	"deepcode/internal/repo/index/analyzer/jvm"
	"deepcode/internal/repo/index/analyzer/markdown"
	"deepcode/internal/repo/index/analyzer/python"
	"deepcode/internal/repo/index/analyzer/rust"
	"deepcode/internal/repo/index/syntax"
)

type Symbol = syntax.Symbol

var comments = syntax.Kinds("comment")

var languages = map[string]*syntax.Grammar{
	"go":         {Language: sitter.NewLanguage(tsgo.Language()), Decl: decl, Facts: goImports, Refs: syntax.Calls, Leading: comments},
	"javascript": {Language: sitter.NewLanguage(tsjs.Language()), Decl: decl, Facts: jsImports, Refs: syntax.Calls, Leading: comments},
	"typescript": {Language: sitter.NewLanguage(tsts.LanguageTypescript()), Decl: decl, Facts: jsImports, Refs: syntax.Calls, Leading: comments},
	"tsx":        {Language: sitter.NewLanguage(tsts.LanguageTSX()), Decl: decl, Facts: jsImports, Refs: syntax.Calls, Leading: comments},
	"csharp":     dotnet.CSharp,
	"java":       jvm.Java,
	"python":     python.Python,
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
	Symbols    []Symbol
	Imports    []string
	References []string // "name@line" of each call, for the name-based call graph
}

// Version is raised whenever what Detect, Generated or Parse make of a file
// changes: a snapshot an older version indexed lends the next none of its
// files' rows, so each is read again. 1 added C#, Java and Rust; 2, JMeter
// test plans; 3, C#'s top-level statements as Main and Markdown's sections;
// 4, JavaScript's functions assigned to members (res.send = function send),
// and a JSDoc a blank line above its declaration; 5, Markdown's generated
// mark and its mdBook includes as imports; 6, Python's functions, classes and
// imports; 7, the names each file calls.
const Version = 7

// HasGrammar reports whether Parse reads lang's declarations and imports with
// a grammar; Markdown's sections and includes it reads without (markdown).
func HasGrammar(lang string) bool { return languages[lang] != nil }

// ReadsImports reports whether Parse reads lang's imports: with a grammar, or
// Markdown's, the files its mdBook directives include.
func ReadsImports(lang string) bool { return HasGrammar(lang) || lang == "markdown" }

// Imported lists the languages ReadsImports reports, sorted.
func Imported() []string {
	out := append(Grammars(), "markdown")
	sort.Strings(out)
	return out
}

// Grammars lists the languages Parse reads, sorted.
func Grammars() []string {
	out := make([]string, 0, len(languages))
	for l := range languages {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Parse reads src as lang, in one pass for its symbols and its imports; a
// Markdown file has its sections, and the files it includes for imports.
func (p *Parser) Parse(lang string, src []byte) File {
	if lang == "markdown" {
		return File{Symbols: markdown.Sections(src), Imports: markdown.Includes(src)}
	}
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
	refs := syntax.Refs(g, tree.RootNode(), src)
	refText := make([]string, len(refs))
	for i, r := range refs {
		refText[i] = r.Name + "@" + strconv.Itoa(r.Line)
	}
	return File{Symbols: syms, Imports: imports, References: refText}
}
