package analyzer

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"deepcode/internal/repo/index/syntax"
)

// decl turns a Go, JavaScript or TypeScript declaration into symbols.
func decl(x *syntax.Extractor, n *sitter.Node) []Symbol {
	switch n.Kind() {
	case "function_declaration", "generator_function_declaration", "function_signature":
		return x.Named(n, n, "function", nil)
	case "method_declaration": // Go
		syms := x.Named(n, n, "method", nil)
		if len(syms) == 1 {
			syms[0].Container = x.FirstOfKind(n.ChildByFieldName("receiver"), "type_identifier")
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
			out = append(out, x.Named(spec, span, kind, nil)...)
		}
		return out
	case "class_declaration", "abstract_class_declaration", "class":
		return x.Named(n, n, "class", x.Block(n.ChildByFieldName("body")))
	case "method_definition", "abstract_method_signature":
		return x.Named(n, n, "method", nil)
	case "public_field_definition", "field_definition": // handler = () => {...}
		if isFunction(n.ChildByFieldName("value")) {
			name := n.ChildByFieldName("name")
			if name == nil {
				name = n.ChildByFieldName("property")
			}
			return x.Symbol(name, n, "method", nil)
		}
	case "interface_declaration":
		return x.Named(n, n, "interface", nil)
	case "type_alias_declaration":
		return x.Named(n, n, "type", nil)
	case "enum_declaration":
		return x.Named(n, n, "enum", nil)
	case "internal_module", "module": // TS namespace / declare module
		return x.Named(n, n, "namespace", x.Block(n.ChildByFieldName("body")))
	case "export_statement":
		inner := n.ChildByFieldName("declaration")
		if inner == nil {
			inner = n.ChildByFieldName("value") // export default function () {}
		}
		if inner == nil {
			return nil
		}
		syms := decl(x, inner)
		if len(syms) == 0 && isFunction(inner) {
			syms = []Symbol{x.Make("default", "function", n, nil)}
		}
		for i := range syms {
			syms[i].StartLine = min(syms[i].StartLine, syntax.Line(n.StartPosition()))
		}
		return syms
	case "ambient_declaration": // declare function / declare namespace
		return x.Block(n)
	case "expression_statement": // the TS grammar wraps `namespace X {}` in one; JS assigns members in one
		switch c := n.NamedChild(0); {
		case c == nil:
		case c.Kind() == "internal_module":
			return decl(x, c)
		case c.Kind() == "assignment_expression":
			return member(x, n, c)
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
			out = append(out, x.Symbol(d.ChildByFieldName("name"), span, kind, nil)...)
		}
		return out
	}
	return nil
}

// member is a function, or class, assigned to members in statement stmt, as
// express defines its request and response: res.send = function send() {},
// x.y = () => {}, X.prototype.y = function () {}, exports.y = function () {},
// and each member of a chain, req.get = req.header = function header() {}.
// Each is y, of x (Container, which qualifies its name): X for X.prototype.y,
// module.exports for module.exports.y. A member whose object is no name or
// member (f().y, x[k]) makes none, nor does a plain name (a = function).
func member(x *syntax.Extractor, stmt, a *sitter.Node) []Symbol {
	var lefts []*sitter.Node
	v := a
	for v != nil && v.Kind() == "assignment_expression" {
		lefts, v = append(lefts, v.ChildByFieldName("left")), unwrap(v.ChildByFieldName("right"))
	}
	if !isFunction(v) {
		return nil
	}
	kind := "function"
	if v.Kind() == "class" {
		kind = "class"
	}
	var out []Symbol
	for _, left := range lefts {
		obj := left.ChildByFieldName("object")
		if obj == nil || obj.Kind() != "identifier" && obj.Kind() != "member_expression" {
			continue
		}
		syms := x.Symbol(left.ChildByFieldName("property"), stmt, kind, nil)
		for i := range syms {
			syms[i].Container = strings.TrimSuffix(x.Text(obj), ".prototype")
		}
		out = append(out, syms...)
	}
	return out
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

// isFunction reports whether JS/TS node n is a function or class value.
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
