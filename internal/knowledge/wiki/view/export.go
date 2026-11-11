package view

import (
	"fmt"
	"strconv"
	"strings"

	"deepcode/internal/repo/gitserver"
)

// File is one file of an exported wiki: its name in the zip and its text.
type File struct{ Name, Body string }

// Export writes the wiki as Markdown files and a README index, the pages in
// their order and a section's pages indented under it, and every page's
// citations linked at the wiki's commit.
func Export(w Wiki, kind, webURL string) []File {
	nums := numbers(w.Pages)
	seen := map[string]bool{}
	files := make([]File, 0, len(w.Pages)+1)
	var readme strings.Builder
	fmt.Fprintf(&readme, "# Wiki\n\nCommit: `%s`\n\n", w.CommitSHA)
	for i, p := range w.Pages {
		name := p.Slug + ".md"
		if p.Slug == "" {
			name = fmt.Sprintf("page-%d.md", i+1)
		}
		indent := ""
		if p.Parent != "" && seen[p.Parent] {
			indent = "  "
		}
		fmt.Fprintf(&readme, "%s- [%s %s](%s)\n", indent, nums[i], p.Title, name)
		seen[p.Slug] = true
		files = append(files, File{Name: name, Body: pageText(w, p, kind, webURL)})
	}
	return append([]File{{Name: "README.md", Body: readme.String()}}, files...)
}

// pageText is p's Markdown: its title, its text, the overview's diagram and
// its citations, each linked at the wiki's commit.
func pageText(w Wiki, p Page, kind, webURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s", p.Title, p.Content)
	if p.Diagram != "" {
		fmt.Fprintf(&b, "\n\n```mermaid\n%s\n```", p.Diagram)
	}
	if len(p.Citations) > 0 {
		b.WriteString("\n\n## Sources\n\n")
		for _, c := range p.Citations {
			link := gitserver.SourceURL(kind, webURL, w.CommitSHA, c.Path, c.StartLine, c.EndLine)
			fmt.Fprintf(&b, "- [%s:%d-%d](%s)\n", c.Path, c.StartLine, c.EndLine, link)
		}
	}
	return b.String()
}

// numbers are the pages' numbers, as the navigation shows them: 1, 2, … at
// the top, and 2.1, 2.2, … under the page numbered 2.
func numbers(pages []Page) []string {
	of := map[string]string{} // by slug
	under := map[string]int{} // pages numbered so far under each section
	top := 0
	out := make([]string, len(pages))
	for i, p := range pages {
		section, ok := of[p.Parent]
		switch {
		case p.Parent == "" || !ok:
			top++
			out[i] = strconv.Itoa(top)
		default:
			k := under[p.Parent] + 1
			under[p.Parent] = k
			out[i] = section + "." + strconv.Itoa(k)
		}
		of[p.Slug] = out[i]
	}
	return out
}
