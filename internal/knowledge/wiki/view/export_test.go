package view

import (
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/evidence"
)

// TestExport: the pages become one Markdown file each, in their order, under
// a README index numbered as the navigation is, indented under their section;
// each page's title opens it, the overview's diagram follows it, and every
// citation links at the wiki's commit.
func TestExport(t *testing.T) {
	w := Wiki{CommitSHA: "abcdef1234567890", Pages: []Page{
		{Slug: "1-overview", Title: "Overview", Content: "About it.", Diagram: "flowchart TD\n  n0[a]"},
		{Slug: "2-router", Title: "Router", Content: "It routes."},
		{Slug: "2.1-tree", Title: "Tree", Content: "A tree.", Parent: "2-router",
			Citations: []evidence.Citation{{Path: "src/tree.go", StartLine: 3, EndLine: 9}}},
	}}
	files := Export(w, "github", "https://github.com/o/r")
	if len(files) != 4 || files[0].Name != "README.md" {
		t.Fatalf("files %v", files)
	}
	readme := files[0].Body
	for _, want := range []string{"Commit: `abcdef1234567890`", "- [1 Overview](1-overview.md)", "- [2 Router](2-router.md)", "  - [2.1 Tree](2.1-tree.md)"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}
	overview := files[1].Body
	if files[1].Name != "1-overview.md" || !strings.Contains(overview, "# Overview\n\nAbout it.") || !strings.Contains(overview, "```mermaid\nflowchart TD") {
		t.Errorf("overview %q: %q", files[1].Name, overview)
	}
	tree := files[3].Body
	if files[3].Name != "2.1-tree.md" || !strings.Contains(tree, "# Tree\n\nA tree.") ||
		!strings.Contains(tree, "## Sources") || !strings.Contains(tree, "(https://github.com/o/r/blob/abcdef1234567890/src/tree.go#L3-L9)") {
		t.Errorf("tree %q: %q", files[3].Name, tree)
	}
	if got := files[2].Name; !slices.Contains([]string{"2-router.md"}, got) {
		t.Errorf("section file %q", got)
	}
}

// TestExportNumbers: a page whose section is missing still gets a top number,
// and a page without a slug gets a safe name.
func TestExportNumbers(t *testing.T) {
	w := Wiki{CommitSHA: "abc", Pages: []Page{{Title: "A"}, {Slug: "2-b", Title: "B", Parent: "nope"}, {Slug: "2.1-c", Title: "C", Parent: "2-b"}}}
	files := Export(w, "gitlab", "https://gitlab.com/o/r")
	if files[1].Name != "page-1.md" || files[2].Name != "2-b.md" || files[3].Name != "2.1-c.md" {
		t.Fatalf("names %q %q %q", files[1].Name, files[2].Name, files[3].Name)
	}
	readme := files[0].Body
	for _, want := range []string{"- [1 A](page-1.md)", "- [2 B](2-b.md)", "  - [2.1 C](2.1-c.md)"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}
}
