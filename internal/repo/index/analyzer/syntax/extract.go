package syntax

import (
	"bytes"
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// A file's facts are bounded, in count and in bytes each: past these it is
// data, not code someone reads, and its facts would only swell the index.
const (
	maxFacts = 200
	maxFact  = 300
)

// Extract reads the symbols and facts of a file that p parsed into root.
func Extract(g *Grammar, root *sitter.Node, src []byte) ([]Symbol, []string) {
	x := &Extractor{Src: src, g: g}
	var facts []string
	if g.Facts != nil {
		facts = clean(g.Facts(x, root))
	}
	return x.Block(root), facts
}

// Parse parses src with g and extracts it, with a parser of its own: for
// tests, which have no Parser to share.
func Parse(g *Grammar, src []byte) ([]Symbol, []string) {
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(g.Language); err != nil {
		return nil, nil
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		return nil, nil
	}
	defer tree.Close()
	return Extract(g, tree.RootNode(), src)
}

// clean keeps each fact once, in order, up to maxFacts, leaving out those too
// long or holding a control character: a line break above all would break the
// list the index keeps.
func clean(facts []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range facts {
		if f == "" || len(f) > maxFact || seen[f] || strings.ContainsFunc(f, unicode.IsControl) {
			continue
		}
		seen[f] = true
		if out = append(out, f); len(out) == maxFacts {
			break
		}
	}
	return out
}

// Extractor walks a syntax tree and turns declarations into Symbols.
type Extractor struct {
	Src []byte
	g   *Grammar
}

// Block extracts the declarations directly inside a file, class body or
// namespace body, attaching each one's leading comments and attributes.
func (x *Extractor) Block(n *sitter.Node) []Symbol {
	if n == nil || n.Parent() == nil {
		return x.block(n, 0, 0)
	}
	return x.block(n, 0, Line(n.StartPosition())) // class A { // a note on the brace's line
}

// block extracts n's declarations from its child from on; a comment starting
// on line prevEnd trails what came before.
func (x *Extractor) block(n *sitter.Node, from uint, prevEnd int) []Symbol {
	if n == nil {
		return nil
	}
	var out []Symbol
	docStart, docEnd := 0, 0
	for i := from; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		start, end := Line(c.StartPosition()), lastLine(c)
		if x.g.Leading[c.Kind()] {
			if start == prevEnd || x.FirstOfKind(c, "inner_doc_comment_marker") != "" { // trails the line before it (int a; // the a), or is Rust's //! about what holds it
				docStart, prevEnd = 0, end
				continue
			}
			if docStart == 0 || start > docEnd+1 {
				docStart = start
			}
			docEnd = end
			continue
		}
		syms := x.g.Decl(x, c)
		if len(syms) == 1 && c.Kind() == x.g.Scope {
			syms[0].Children = x.block(n, i+1, end) // namespace A; // a note
			if last := n.NamedChild(n.NamedChildCount() - 1); last != nil {
				syms[0].EndLine = max(syms[0].EndLine, lastLine(last))
			}
			i = n.NamedChildCount() // the scope runs to the end of n
		}
		// A symbol that starts inside the node (the first type of a Go type
		// group, the first method of a Rust impl block) carries the node's comment.
		if len(syms) > 0 && docStart > 0 && docEnd >= start-1 {
			syms[0].StartLine = min(syms[0].StartLine, docStart)
		}
		docStart, prevEnd = 0, end
		out = append(out, syms...)
	}
	return out
}

// lastLine is the line n ends on; a node that takes its line break along (a
// Rust // comment, C#'s #region) ends on the line before.
func lastLine(n *sitter.Node) int {
	end := Line(n.EndPosition())
	if n.EndPosition().Column == 0 && end > Line(n.StartPosition()) {
		end--
	}
	return end
}

// Text is n's source text, "" for no node.
func (x *Extractor) Text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Utf8Text(x.Src)
}

// Named builds a symbol from n's "name" field, spanning span.
func (x *Extractor) Named(n, span *sitter.Node, kind string, children []Symbol) []Symbol {
	return x.Symbol(n.ChildByFieldName("name"), span, kind, children)
}

// Symbol builds a symbol named by the node name, spanning span, its signature
// the line holding the name: a declaration's first line is often an attribute,
// annotation or decorator ([HttpGet], @Override) that says little. None
// without a whole name: none at all, or one a broken parse cut short (A.).
func (x *Extractor) Symbol(name, span *sitter.Node, kind string, children []Symbol) []Symbol {
	if name == nil || name.HasError() || x.Text(name) == "" {
		return nil
	}
	s := x.Make(x.Text(name), kind, span, children)
	s.Signature = x.LineOf(span, name)
	return []Symbol{s}
}

// Make builds a symbol spanning span, its signature span's first line.
func (x *Extractor) Make(name, kind string, span *sitter.Node, children []Symbol) Symbol {
	// Only the first line is copied: deeply nested symbols each span most of the file.
	first, _, _ := bytes.Cut(x.Src[span.StartByte():span.EndByte()], []byte("\n"))
	return Symbol{
		Name: name, Kind: kind, Signature: signature(first), Children: children,
		StartLine: Line(span.StartPosition()), EndLine: Line(span.EndPosition()),
	}
}

// LineOf is the signature the source line holding at's start makes, from no
// earlier than from's start: a one-line class's second method is not its first.
func (x *Extractor) LineOf(from, at *sitter.Node) string {
	start := max(uint(bytes.LastIndexByte(x.Src[:at.StartByte()], '\n')+1), from.StartByte())
	line, _, _ := bytes.Cut(x.Src[start:], []byte("\n"))
	return signature(line)
}

// signature is a line trimmed and cut to 160 bytes, never inside a character.
func signature(line []byte) string {
	sig := strings.TrimSpace(string(line))
	if len(sig) > 160 {
		sig = strings.ToValidUTF8(sig[:160], "")
	}
	return sig
}

// FirstOfKind is the text of the first node of kind in n, n included, depth first.
func (x *Extractor) FirstOfKind(n *sitter.Node, kind string) string {
	if n == nil {
		return ""
	}
	if n.Kind() == kind {
		return x.Text(n)
	}
	for i := range n.NamedChildCount() {
		if s := x.FirstOfKind(n.NamedChild(i), kind); s != "" {
			return s
		}
	}
	return ""
}

// Line is a position's 1-based line.
func Line(p sitter.Point) int { return int(p.Row) + 1 }
