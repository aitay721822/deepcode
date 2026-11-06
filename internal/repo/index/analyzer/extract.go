package analyzer

import (
	"bytes"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractor walks a Tree-sitter syntax tree and turns declarations into Symbols.
type extractor struct{ src []byte }

// block extracts the declarations directly inside a file, class body or
// namespace body, attaching each one's leading comment run.
func (x *extractor) block(n *sitter.Node) []Symbol {
	if n == nil {
		return nil
	}
	var out []Symbol
	docStart, docEnd := 0, 0
	for i := range n.NamedChildCount() {
		c := n.NamedChild(i)
		start, end := line(c.StartPosition()), line(c.EndPosition())
		if c.Kind() == "comment" {
			if docStart == 0 || start > docEnd+1 {
				docStart = start
			}
			docEnd = end
			continue
		}
		syms := x.decl(c)
		if len(syms) > 0 && docStart > 0 && docEnd >= start-1 {
			syms[0].StartLine = min(syms[0].StartLine, docStart)
		}
		docStart = 0
		out = append(out, syms...)
	}
	return out
}

func (x *extractor) decl(n *sitter.Node) []Symbol {
	switch n.Kind() {
	case "function_declaration", "generator_function_declaration", "function_signature":
		return x.named(n, n, "function", nil)
	case "method_declaration": // Go
		syms := x.named(n, n, "method", nil)
		if len(syms) == 1 {
			syms[0].Container = x.firstOfKind(n.ChildByFieldName("receiver"), "type_identifier")
		}
		return syms
	case "type_declaration": // Go: type ( A struct{}; B interface{} )
		var out []Symbol
		for i := range n.NamedChildCount() {
			spec := n.NamedChild(i)
			if spec.Kind() != "type_spec" && spec.Kind() != "type_alias" {
				continue
			}
			kind := "type"
			if t := spec.ChildByFieldName("type"); t != nil {
				kind = map[string]string{"struct_type": "struct", "interface_type": "interface"}[t.Kind()]
				if kind == "" {
					kind = "type"
				}
			}
			span := spec
			if n.NamedChildCount() == 1 {
				span = n // include the `type` keyword
			}
			out = append(out, x.named(spec, span, kind, nil)...)
		}
		return out
	case "class_declaration", "abstract_class_declaration", "class":
		return x.named(n, n, "class", x.block(n.ChildByFieldName("body")))
	case "method_definition", "abstract_method_signature":
		return x.named(n, n, "method", nil)
	case "public_field_definition", "field_definition": // handler = () => {...}
		if isFunction(n.ChildByFieldName("value")) {
			name := n.ChildByFieldName("name")
			if name == nil {
				name = n.ChildByFieldName("property")
			}
			return x.symbol(name, n, "method", nil)
		}
	case "interface_declaration":
		return x.named(n, n, "interface", nil)
	case "type_alias_declaration":
		return x.named(n, n, "type", nil)
	case "enum_declaration":
		return x.named(n, n, "enum", nil)
	case "internal_module", "module": // TS namespace / declare module
		return x.named(n, n, "namespace", x.block(n.ChildByFieldName("body")))
	case "export_statement":
		inner := n.ChildByFieldName("declaration")
		if inner == nil {
			inner = n.ChildByFieldName("value") // export default function () {}
		}
		if inner == nil {
			return nil
		}
		syms := x.decl(inner)
		if len(syms) == 0 && isFunction(inner) {
			syms = []Symbol{x.make("default", "function", n, nil)}
		}
		for i := range syms {
			syms[i].StartLine = min(syms[i].StartLine, line(n.StartPosition()))
		}
		return syms
	case "ambient_declaration": // declare function / declare namespace
		return x.block(n)
	case "expression_statement": // the TS grammar wraps `namespace X {}` in one
		if c := n.NamedChild(0); c != nil && c.Kind() == "internal_module" {
			return x.decl(c)
		}
	case "lexical_declaration", "variable_declaration": // const f = () => {}
		var out []Symbol
		for i := range n.NamedChildCount() {
			d := n.NamedChild(i)
			v := unwrap(d.ChildByFieldName("value"))
			if d.Kind() != "variable_declarator" || !isFunction(v) {
				continue
			}
			kind := "function"
			if v.Kind() == "class" {
				kind = "class"
			}
			span := d
			if n.NamedChildCount() == 1 {
				span = n
			}
			out = append(out, x.symbol(d.ChildByFieldName("name"), span, kind, nil)...)
		}
		return out
	}
	return nil
}

// named builds a symbol from n's "name" field, spanning span.
func (x *extractor) named(n, span *sitter.Node, kind string, children []Symbol) []Symbol {
	return x.symbol(n.ChildByFieldName("name"), span, kind, children)
}

func (x *extractor) symbol(name, span *sitter.Node, kind string, children []Symbol) []Symbol {
	if name == nil {
		return nil
	}
	return []Symbol{x.make(name.Utf8Text(x.src), kind, span, children)}
}

func (x *extractor) make(name, kind string, span *sitter.Node, children []Symbol) Symbol {
	// Only the first line is copied: deeply nested symbols each span most of the file.
	first, _, _ := bytes.Cut(x.src[span.StartByte():span.EndByte()], []byte("\n"))
	sig := strings.TrimSpace(string(first))
	if len(sig) > 160 {
		sig = strings.ToValidUTF8(sig[:160], "") // never split a character
	}
	return Symbol{
		Name: name, Kind: kind, Signature: sig, Children: children,
		StartLine: line(span.StartPosition()), EndLine: line(span.EndPosition()),
	}
}

func (x *extractor) firstOfKind(n *sitter.Node, kind string) string {
	if n == nil {
		return ""
	}
	if n.Kind() == kind {
		return n.Utf8Text(x.src)
	}
	for i := range n.NamedChildCount() {
		if s := x.firstOfKind(n.NamedChild(i), kind); s != "" {
			return s
		}
	}
	return ""
}

// unwrap peels what TypeScript wraps a value in: ((x) => x) as T, f satisfies T, f!.
func unwrap(n *sitter.Node) *sitter.Node {
	for n != nil {
		switch n.Kind() {
		case "as_expression", "satisfies_expression", "non_null_expression", "parenthesized_expression":
			n = n.NamedChild(0)
		default:
			return n
		}
	}
	return nil
}

func isFunction(n *sitter.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind() {
	case "arrow_function", "function_expression", "function", "generator_function", "class":
		return true
	}
	return false
}

func line(p sitter.Point) int { return int(p.Row) + 1 }
