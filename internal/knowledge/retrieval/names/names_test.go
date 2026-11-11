package names

import (
	"slices"
	"testing"
)

// TestFileNames: a file the query names by path, file name, folder or glob is
// read, punctuation, brackets and lines around it aside; a file in backticks
// is read whole, its spaces kept, and other text in them as any other.
func TestFileNames(t *testing.T) {
	for query, want := range map[string][]string{
		"Focus: (src/app/Host.cs), ./README.md, tests/*.rs; examples/ App.java. Not .NET, 0.25, e.g. or res.send, see https://x.io/a.go, [app/[slug]/page.tsx] and app/(group) twice: src/app/Host.cs": {
			"src/app/Host.cs", "README.md", "tests/*.rs", "examples/", "App.java", "app/[slug]/page.tsx", "app/(group)"},
		"重點：src/a.cs、src/b.cs（見 lib/c.go:42）與「d/e.ts」":                          {"src/a.cs", "src/b.cs", "lib/c.go", "d/e.ts"},
		`**src/a.ts** and src\b\c.go#L10-L20 or (group)/page.tsx. (See x/y.go)`: {"src/a.ts", "src/b/c.go", "(group)/page.tsx", "x/y.go"},
		"(group)/(admin)/page.tsx, src/a.go:42:7, path=src/x.go|src/y.go 請看 App.java？ **src/b.go**. **c/d.go**:": {
			"(group)/(admin)/page.tsx", "src/a.go", "src/x.go", "src/y.go", "App.java", "src/b.go", "c/d.go"},
		"See `src/Events/OrderPaid .cs` and `a.go`, run `go test ./x/...`, `docs/my guide/` or `b c.go\nd.go` then `e f.cs": {
			"src/Events/OrderPaid .cs", "a.go", "x/", "docs/my", "guide/", "c.go", "d.go", "f.cs"},
	} {
		if got := FileNames(query); !slices.Equal(got, want) {
			t.Errorf("FileNames(%q) = %q, want %q", query, got, want)
		}
	}
	for in, want := range map[string]string{"(": "", "()": "", "[[]]": "", "(a]": "a", "(group)/(admin)": "(group)/(admin)", "((a/b))": "a/b", "a/b)": "a/b"} {
		if got := unwrap(in); got != want {
			t.Errorf("unwrap(%q) = %q, want %q", in, got, want)
		}
	}
}
