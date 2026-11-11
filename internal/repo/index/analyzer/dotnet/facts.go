package dotnet

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"deepcode/internal/repo/index/syntax"
)

// facts lists what the graph's dotnet resolver reads of a C# file, in this
// order since syntax keeps only a file's first facts:
//
//	ns:A.B          a namespace it declares, block or file-scoped
//	using:X.Y       using X.Y;
//	global:X.Y      global using X.Y; (for its whole project)
//	static:X.Y.Z    using static X.Y.Z; (global or not)
//	alias:X.Y.Z     using A = X.Y.Z;
//	def:A.B.T       a type it declares, nested ones as def:A.B.Outer.Inner
//	type:T          a type it refers to, type:A.T when qualified
func facts(x *syntax.Extractor, root *sitter.Node) []string {
	var heads, defs []string
	declared(x, root, "", &heads, &defs)
	return append(append(heads, defs...), refs(x, root, nil)...)
}

// declared adds the namespaces, usings and types declared in n, a file or a
// namespace or type body, to heads and defs; prefix is n's qualified name.
func declared(x *syntax.Extractor, n *sitter.Node, prefix string, heads, defs *[]string) {
	if n == nil {
		return
	}
	for i := range n.NamedChildCount() {
		c := n.NamedChild(i)
		switch k := c.Kind(); {
		case k == "using_directive":
			*heads = append(*heads, using(x, c))
		case k == "namespace_declaration":
			ns := prefix
			if name := dotted(x, c.ChildByFieldName("name")); name != "" { // else broken: namespace A. { }
				ns = join(prefix, name)
				*heads = append(*heads, "ns:"+ns)
			}
			declared(x, c.ChildByFieldName("body"), ns, heads, defs)
		case k == "file_scoped_namespace_declaration": // it holds the declarations after it
			if name := dotted(x, c.ChildByFieldName("name")); name != "" {
				prefix = join(prefix, name)
				*heads = append(*heads, "ns:"+prefix)
			}
		case k == "preproc_if" || k == "preproc_elif" || k == "preproc_else":
			declared(x, c, prefix, heads, defs)
		case types[k] != "":
			if name := x.Text(c.ChildByFieldName("name")); name != "" {
				q := join(prefix, name)
				*defs = append(*defs, "def:"+q)
				declared(x, c.ChildByFieldName("body"), q, heads, defs)
			}
		}
	}
}

// using is a using directive's fact, "" when it names no namespace or type
// (using P = (int, int);). Neither global nor static has a node of its own.
func using(x *syntax.Extractor, n *sitter.Node) string {
	if n.NamedChildCount() == 0 {
		return ""
	}
	target := dotted(x, n.NamedChild(n.NamedChildCount()-1))
	words := strings.Fields(x.Text(n))
	switch {
	case target == "":
		return ""
	case n.ChildByFieldName("name") != nil:
		return "alias:" + target
	case slices.Contains(words, "static"):
		return "static:" + target
	case words[0] == "global":
		return "global:" + target
	}
	return "using:" + target
}

// refs adds the types n and its descendants refer to: names in type
// positions, and the left of a member access when it starts with a capital
// (Helper.Format, Color.Red).
// ponytail: without semantic analysis a capitalised property (Id.ToString())
// reads as a type too; it becomes an edge only if a type of that name is in
// scope. Read the members a file declares if that proves noisy.
func refs(x *syntax.Extractor, n *sitter.Node, out []string) []string {
	for i := range n.NamedChildCount() {
		c := n.NamedChild(i)
		if typed(n.Kind(), n.FieldNameForNamedChild(uint32(i))) {
			if name := dotted(x, c); name != "" {
				if r, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(r) {
					out = append(out, "type:"+name)
					if n.Kind() == "attribute" && !strings.HasSuffix(name, "Attribute") {
						out = append(out, "type:"+name+"Attribute") // [Serializable] is SerializableAttribute
					}
				}
				if c.Kind() == "member_access_expression" {
					continue // A.B.C: A and A.B are no references of their own
				}
			}
		}
		out = refs(x, c, out)
	}
	return out
}

// typed reports whether a child at field of a parent node is in a type
// position, or the object of a member access.
func typed(parent, field string) bool {
	switch field {
	case "type", "returns":
		return true
	case "expression":
		return parent == "member_access_expression"
	case "right": // x as T, x is T
		return parent == "as_expression" || parent == "is_expression"
	case "name":
		return parent == "attribute"
	case "": // constant_pattern: x is T, case T
		switch parent {
		case "base_list", "type_argument_list", "explicit_interface_specifier", "constant_pattern":
			return true
		}
	}
	return false
}

// dotted is a name's text without type arguments or an alias:: qualifier
// (A.B.C for global::A.B.C<T>), "" for anything that is not a name or a
// member access of names.
func dotted(x *syntax.Extractor, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "identifier":
		return x.Text(n)
	case "generic_name":
		return dotted(x, n.NamedChild(0))
	case "alias_qualified_name":
		return dotted(x, n.ChildByFieldName("name"))
	case "qualified_name", "member_access_expression":
		left := n.ChildByFieldName("qualifier")
		if left == nil {
			left = n.ChildByFieldName("expression")
		}
		a, b := dotted(x, left), dotted(x, n.ChildByFieldName("name"))
		if a == "" || b == "" {
			return ""
		}
		return a + "." + b
	}
	return ""
}

// join qualifies name with prefix, if any.
func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
