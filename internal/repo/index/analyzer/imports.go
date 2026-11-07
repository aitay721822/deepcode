package analyzer

import (
	"strconv"
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// A file's imports are bounded, in count and in bytes each: past these it is
// data, not code someone reads, and its imports would only swell the index.
const (
	maxImports = 200
	maxSpec    = 300
)

// imports lists the modules a file imports, as its source spells them, once
// each and in order: Go's import paths, and the specifiers of JavaScript's
// and TypeScript's import and export-from statements, import x = require(),
// and require() and import() called with a string.
func (x *extractor) imports(root *sitter.Node, lang string) []string {
	var specs []string
	if lang == "go" {
		for i := range root.NamedChildCount() {
			if d := root.NamedChild(i); d.Kind() == "import_declaration" {
				specs = x.goImports(d, specs)
			}
		}
	} else {
		specs = x.jsImports(root, specs)
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range specs {
		if s == "" || len(s) > maxSpec || seen[s] || strings.ContainsFunc(s, unicode.IsControl) {
			continue // a control character, a line break above all, would break the list the index keeps
		}
		seen[s] = true
		if out = append(out, s); len(out) == maxImports {
			break
		}
	}
	return out
}

// goImports adds the paths of an import declaration's specs: one, or a
// parenthesised list.
func (x *extractor) goImports(n *sitter.Node, specs []string) []string {
	for i := range n.NamedChildCount() {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "import_spec_list":
			specs = x.goImports(c, specs)
		case "import_spec":
			if p := c.ChildByFieldName("path"); p != nil {
				if s, err := strconv.Unquote(p.Utf8Text(x.src)); err == nil {
					specs = append(specs, s)
				}
			}
		}
	}
	return specs
}

// jsImports adds the specifiers n and its descendants import: require() and
// import() can sit anywhere, inside a function as well.
func (x *extractor) jsImports(n *sitter.Node, specs []string) []string {
	switch n.Kind() {
	case "import_statement", "export_statement":
		src := n.ChildByFieldName("source")
		for i := uint(0); src == nil && i < n.NamedChildCount(); i++ {
			if c := n.NamedChild(i); c.Kind() == "import_require_clause" {
				src = c.ChildByFieldName("source")
			}
		}
		if src != nil {
			specs = append(specs, x.str(src))
		}
	case "call_expression":
		fn := n.ChildByFieldName("function")
		if fn != nil && (fn.Kind() == "import" || fn.Kind() == "identifier" && fn.Utf8Text(x.src) == "require") {
			if args := n.ChildByFieldName("arguments"); args != nil && args.NamedChildCount() > 0 {
				specs = append(specs, x.str(args.NamedChild(0)))
			}
		}
	}
	for i := range n.NamedChildCount() {
		specs = x.jsImports(n.NamedChild(i), specs)
	}
	return specs
}

// str is a string literal's text without its quotes, or "" for any other node.
func (x *extractor) str(n *sitter.Node) string {
	if n.Kind() != "string" {
		return ""
	}
	t := n.Utf8Text(x.src)
	if len(t) < 2 {
		return ""
	}
	return t[1 : len(t)-1]
}
