package syntax

import (
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Refs reads the calls of a file that p parsed into root, for a name-based
// call graph: each call's callee name, an attribute's or member's last part,
// and the line it is made on.
func Refs(g *Grammar, root *sitter.Node, src []byte) []Reference {
	if g.Refs == nil {
		return nil
	}
	return g.Refs(&Extractor{Src: src, g: g}, root)
}

// Calls is a language's calls: every call node's callee name and line.
func Calls(x *Extractor, root *sitter.Node) []Reference {
	var out []Reference
	seen := map[string]bool{}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if isCall(n.Kind()) {
			if name := callee(x, callOf(n)); name != "" {
				key := name + "@" + itoa(int(n.StartPosition().Row)+1)
				if len(out) < maxRefs && !seen[key] && !strings.ContainsFunc(name, unicode.IsControl) {
					seen[key] = true
					out = append(out, Reference{Name: name, Line: int(n.StartPosition().Row) + 1})
				}
			}
		}
		for i := range n.NamedChildCount() {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
	return out
}

// maxRefs bounds a file's calls, as facts are bounded: past it the file is
// data, and the edges it would make are noise.
const maxRefs = 2000

// isCall reports whether kind is a call node, across the grammars.
func isCall(kind string) bool {
	switch kind {
	case "call", "call_expression", "method_invocation", "invocation_expression",
		"object_creation_expression", "object_creation", "new_expression":
		return true
	}
	return false
}

// callOf is the node naming the call's callee: its function or constructor,
// else its name.
func callOf(n *sitter.Node) *sitter.Node {
	for _, field := range []string{"function", "method", "name", "type", "constructor"} {
		if c := n.ChildByFieldName(field); c != nil {
			return c
		}
	}
	return n
}

// callee is the last name in a call's callee: ServeHTTP in s.ServeHTTP,
// Tree in router.Tree(), User in User().
func callee(x *Extractor, n *sitter.Node) string {
	for guard := 0; n != nil && guard < 20; guard++ {
		kind := n.Kind()
		if kind == "identifier" || strings.HasSuffix(kind, "_identifier") {
			return x.Text(n)
		}
		var next *sitter.Node
		for _, field := range []string{"name", "field", "property", "attribute", "function", "type", "constructor"} {
			if c := n.ChildByFieldName(field); c != nil && c.Id() != n.Id() {
				next = c
				break
			}
		}
		if next == nil { // the last named child that is not the arguments
			for i := int(n.NamedChildCount()) - 1; i >= 0; i-- {
				if c := n.NamedChild(uint(i)); c.Kind() != "argument_list" && c.Kind() != "arguments" && c.Kind() != "type_arguments" {
					next = c
					break
				}
			}
		}
		if next == nil || next.Id() == n.Id() {
			return ""
		}
		n = next
	}
	return ""
}

// itoa is a small positive int as text, avoiding strconv in this hot walk.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
