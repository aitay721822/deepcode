// Package markdown reads the sections of a Markdown file as symbols, so that
// a guide is chunked, searched and planned by its headings as code is by its
// declarations: a README's recipes each a subject of their own.
package markdown

import (
	"regexp"
	"strings"

	"deepcode/internal/repo/index/analyzer/syntax"
)

var (
	// headingRe matches an ATX heading: up to three spaces, one to six #, then
	// the title, a closing run of # aside.
	headingRe = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))??(?:[ \t]+#+)?[ \t]*$`)
	// fenceRe matches a code block's fence: its mark, and what follows it.
	fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
)

// maxTitle is the most characters of a heading a section's name keeps.
const maxTitle = 120

// heading is a heading's line, level and title.
type heading struct {
	line, level int
	title, text string
}

// Sections are the file's sections: each heading outside a code block and
// a docs site's front matter (a first line --- to the next --- or ...) names
// the lines from it to the next heading of its level or above, those of the
// deeper headings in between its children. A heading with no title, or a
// setext one (underlined with = or -), names none.
// ponytail: ATX headings only, the kind READMEs and docs sites use; read
// setext ones if guides that use them chunk badly.
func Sections(src []byte) []syntax.Symbol {
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n"), "\n")
	var hs []heading
	fence, start := "", 0 // the open code block's mark; the first line after the front matter
	if strings.TrimRight(lines[0], " \t") == "---" {
		for j := 1; j < len(lines) && start == 0; j++ {
			if t := strings.TrimRight(lines[j], " \t"); t == "---" || t == "..." {
				start = j + 1
			}
		}
	}
	for i := start; i < len(lines); i++ {
		l := lines[i]
		if m := fenceRe.FindStringSubmatch(l); m != nil {
			switch {
			case fence == "" && !(m[1][0] == '`' && strings.Contains(m[2], "`")): // a backtick in its info string makes it inline code
				fence = m[1]
			case fence != "" && m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(m[2]) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if m := headingRe.FindStringSubmatch(l); m != nil && strings.TrimSpace(m[2]) != "" {
			title := []rune(strings.Join(strings.Fields(m[2]), " "))
			hs = append(hs, heading{line: i + 1, level: len(m[1]), title: string(title[:min(len(title), maxTitle)]), text: l})
		}
	}
	return build(hs, len(lines))
}

// build turns headings into sections ending by end at the latest.
func build(hs []heading, end int) []syntax.Symbol {
	var out []syntax.Symbol
	for i := 0; i < len(hs); {
		h, j := hs[i], i+1
		for j < len(hs) && hs[j].level > h.level {
			j++
		}
		stop := end
		if j < len(hs) {
			stop = hs[j].line - 1
		}
		out = append(out, syntax.Symbol{Name: h.title, Kind: "section", StartLine: h.line, EndLine: stop,
			Signature: strings.TrimSpace(h.text), Children: build(hs[i+1:j], stop)})
		i = j
	}
	return out
}
