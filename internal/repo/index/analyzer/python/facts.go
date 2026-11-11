package python

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"deepcode/internal/repo/index/syntax"
)

// facts lists what a Python file imports, as the graph's resolver reads
// them: "module:a.b" for `import a.b` and "name:a.b.C" for
// `from a.b import C`; a relative import keeps its dots, as ".models" or
// "..models". Aliases are dropped, and __future__ is left out.
func facts(x *syntax.Extractor, root *sitter.Node) []string {
	var out []string
	walk(x, root, &out)
	return out
}

// walk reads the import statements of n and its descendants: Python allows
// one inside a function.
func walk(x *syntax.Extractor, n *sitter.Node, out *[]string) {
	switch n.Kind() {
	case "import_statement":
		for i := range n.NamedChildCount() {
			if name := module(x, n.NamedChild(i)); name != "" {
				*out = append(*out, "module:"+name)
			}
		}
		return
	case "import_from_statement":
		moduleNode := n.ChildByFieldName("module_name")
		mod := ""
		if moduleNode != nil {
			if moduleNode.Kind() == "relative_import" {
				mod = x.Text(moduleNode)
			} else if name := module(x, moduleNode); name != "" {
				mod = name
			}
		}
		if mod == "__future__" {
			return
		}
		if mod != "" {
			*out = append(*out, "from:"+mod)
		}
		for i := range n.NamedChildCount() {
			child := n.NamedChild(i)
			if moduleNode != nil && child.Id() == moduleNode.Id() || child.Kind() == "wildcard_import" {
				continue
			}
			if name := module(x, child); name != "" {
				*out = append(*out, "name:"+join(mod, name))
			}
		}
		return
	}
	for i := range n.NamedChildCount() {
		walk(x, n.NamedChild(i), out)
	}
}

// module is the module a named import node names, an alias's name dropped.
func module(x *syntax.Extractor, n *sitter.Node) string {
	if n == nil || n.Kind() == "wildcard_import" {
		return ""
	}
	name, _, _ := strings.Cut(x.Text(n), " as ")
	return strings.TrimSpace(name)
}

// join puts a name under module, with the dot a relative module already has.
func join(module, name string) string {
	if module == "" || strings.HasSuffix(module, ".") {
		return module + name
	}
	return module + "." + name
}
