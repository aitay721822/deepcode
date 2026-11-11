package outline

import (
	"slices"
	"strings"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/analyzer/markdown"
)

// Recipes are the subjects of the sections of README text src that show
// code (Shown), by title.
func Recipes(src string) []string {
	var out []string
	for _, s := range Shown(src) {
		out = append(out, s.Title)
	}
	return out
}

// Section is a section of Markdown text: its title and the line its heading
// is on.
type Section struct {
	Title string
	Line  int
}

// Shown are the sections of README or guide text src, its # and ## headings,
// that show code, a fenced block before the next of those: each title up to
// its first ( ? ! : , or ;, "Transient updates" for zustand's "Transient
// updates (for often occurring state-changes)", each once. A section of
// prose, features, credits or a licence, is no subject of the code's, nor is
// a ### one's, Steps or Optional, nor the text's # title, a project's name
// over its install line or a guide's own.
func Shown(src string) []Section {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []Section
	var walk func([]analyzer.Symbol)
	walk = func(ss []analyzer.Symbol) {
		for _, s := range ss {
			if strings.HasPrefix(s.Signature, "###") {
				continue
			}
			end := s.EndLine
			if i := slices.IndexFunc(s.Children, func(c analyzer.Symbol) bool { return !strings.HasPrefix(c.Signature, "###") }); i >= 0 {
				end = s.Children[i].StartLine - 1
			}
			own := strings.Join(lines[min(s.StartLine, len(lines)):min(end, len(lines))], "\n") // after its heading line
			title := strings.NewReplacer("`", "", "*", "").Replace(s.Name)
			if i := strings.IndexAny(title, "(?!:,;"); i >= 0 {
				title = title[:i]
			}
			if title = strings.TrimSpace(title); title != "" && (strings.Contains(own, "```") || strings.Contains(own, "~~~")) &&
				!slices.ContainsFunc(out, func(o Section) bool { return o.Title == title }) {
				out = append(out, Section{Title: title, Line: s.StartLine})
			}
			walk(s.Children)
		}
	}
	secs := markdown.Sections([]byte(src))
	if len(secs) > 0 && strings.HasPrefix(secs[0].Signature, "# ") { // the title: its sections only
		walk(secs[0].Children)
		secs = secs[1:]
	}
	walk(secs)
	return out
}

// Title is the # title Markdown text src opens with, and its line; "" and 0
// for none.
func Title(src string) (string, int) {
	if secs := markdown.Sections([]byte(src)); len(secs) > 0 && strings.HasPrefix(secs[0].Signature, "# ") {
		return OneLine(strings.NewReplacer("`", "", "*", "").Replace(secs[0].Name)), secs[0].StartLine
	}
	return "", 0
}
