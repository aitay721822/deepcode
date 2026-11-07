package analyzer

import (
	"strconv"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"deepcode/internal/repo/index/analyzer/syntax"
)

// goImports lists the import paths of a Go file's import declarations.
func goImports(x *syntax.Extractor, root *sitter.Node) []string {
	var specs []string
	for i := range root.NamedChildCount() {
		if d := root.NamedChild(i); d.Kind() == "import_declaration" {
			specs = goSpecs(x, d, specs)
		}
	}
	return specs
}

// goSpecs adds the paths of an import declaration's specs: one, or a
// parenthesised list.
func goSpecs(x *syntax.Extractor, n *sitter.Node, specs []string) []string {
	for i := range n.NamedChildCount() {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "import_spec_list":
			specs = goSpecs(x, c, specs)
		case "import_spec":
			if p := c.ChildByFieldName("path"); p != nil {
				if s, err := strconv.Unquote(x.Text(p)); err == nil {
					specs = append(specs, s)
				}
			}
		}
	}
	return specs
}

// jsImports lists the specifiers of JavaScript's and TypeScript's import and
// export-from statements, import x = require(), and require() and import()
// called with a string, n and its descendants: require() and import() can sit
// anywhere, inside a function as well.
func jsImports(x *syntax.Extractor, n *sitter.Node) []string { return jsSpecs(x, n, nil) }

// jsSpecs adds to specs the module specifiers that n and its descendants import.
func jsSpecs(x *syntax.Extractor, n *sitter.Node, specs []string) []string {
	switch n.Kind() {
	case "import_statement", "export_statement":
		src := n.ChildByFieldName("source")
		for i := uint(0); src == nil && i < n.NamedChildCount(); i++ {
			if c := n.NamedChild(i); c.Kind() == "import_require_clause" {
				src = c.ChildByFieldName("source")
			}
		}
		if src != nil {
			specs = append(specs, str(x, src))
		}
	case "call_expression":
		fn := n.ChildByFieldName("function")
		if fn != nil && (fn.Kind() == "import" || fn.Kind() == "identifier" && x.Text(fn) == "require") {
			if args := n.ChildByFieldName("arguments"); args != nil && args.NamedChildCount() > 0 {
				specs = append(specs, str(x, args.NamedChild(0)))
			}
		}
	}
	for i := range n.NamedChildCount() {
		specs = jsSpecs(x, n.NamedChild(i), specs)
	}
	return specs
}

// str is a string literal's text without its quotes, or "" for any other node.
func str(x *syntax.Extractor, n *sitter.Node) string {
	if n.Kind() != "string" {
		return ""
	}
	t := x.Text(n)
	if len(t) < 2 {
		return ""
	}
	return t[1 : len(t)-1]
}
