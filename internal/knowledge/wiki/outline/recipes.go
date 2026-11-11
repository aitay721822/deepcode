package outline

import (
	"slices"
	"strings"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/analyzer/markdown"
)

// Recipes are the subjects of the sections of README text src, its # and ##
// headings, that show code, a fenced block before the next of those: each
// title up to its first ( ? ! : , or ;, "Transient updates" for zustand's
// "Transient updates (for often occurring state-changes)". A section of
// prose, features, credits or a licence, is no subject of the code's, nor is
// a ### one's, Steps or Optional, nor the README's # title, the project's
// name over its install line.
func Recipes(src string) []string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []string
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
			if title = strings.TrimSpace(title); title != "" && (strings.Contains(own, "```") || strings.Contains(own, "~~~")) && !slices.Contains(out, title) {
				out = append(out, title)
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
