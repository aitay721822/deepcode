package syntax

import (
	"reflect"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// TestCalls: a call's callee is its function's last name, be it a plain
// function, a method or a module's function, at the line it is made.
func TestCalls(t *testing.T) {
	src := []byte(`def f():
    g()
    obj.handle()
    pkg.util.run(1)
    x = f
`)
	g := &Grammar{Language: sitter.NewLanguage(tspython.Language()), Refs: Calls}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(g.Language); err != nil {
		t.Fatal(err)
	}
	tree := p.Parse(src, nil)
	defer tree.Close()
	got := Refs(g, tree.RootNode(), src)
	want := []Reference{{"g", 2}, {"handle", 3}, {"run", 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls %+v, want %+v", got, want)
	}
}
