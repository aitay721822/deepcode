package evidence

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"deepcode/internal/knowledge/retrieval"
)

type Citation struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

var (
	// citeRe matches one bracket group, optionally already turned into a
	// markdown link by the model (whose target is discarded), or else one bare
	// reference, path:10-20 or `path:10-20` (its third group), its path in ASCII,
	// so that text running up to it (見a.go:3) stays out.
	citeRe = regexp.MustCompile("\\[([^\\[\\]\\n]+)\\](\\([^)\\s]*\\))?|(`[\\w./@+~$-]+?:L?\\d+(?:-L?\\d+)?`|[\\w./@+~$-]+?:L?\\d+(?:-L?\\d+)?)")
	// inlineRe matches an inline code span, outside code blocks.
	inlineRe = regexp.MustCompile("`[^`\n]+`")
	// refRe matches one reference inside a group: path:10-20, path:10 or
	// path:L10-L20, optionally in backticks.
	refRe = regexp.MustCompile("^`?([^\\s`]+?):L?(\\d+)(?:-L?(\\d+))?`?$")
	// rangeRe matches a bare range that continues the previous reference's
	// file, like the 99-100 in [a.go:43-51, 99-100].
	rangeRe = regexp.MustCompile(`^L?\d+(?:-L?\d+)?$`)
	// labelRe matches a label some models put before a reference: [source: a.go:3-9].
	labelRe = regexp.MustCompile(`(?i)^sources?:\s*`)
	// pathlessRe matches lines cited without their file, [55-64], or after a
	// source's number, [5:1-29].
	pathlessRe = regexp.MustCompile(`\[(?:\d+:)?L?\d+(?:-L?\d+)?\]`)
	// fenceRe matches a fenced code block, or one left open at the end.
	fenceRe = regexp.MustCompile("(?s)```.*?(?:```|$)")
	// linkText escapes what would end a link's text early: a path may hold
	// brackets (app/[slug]/page.tsx) or, rarely, a backslash or line break.
	linkText = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`, "\n", " ", "\r", " ")
)

type ref struct {
	path       string
	start, end int
}

// LinkCitations checks every citation group against the evidence that was
// actually shown to the model. A group holds one or more references separated
// by commas or semicolons, e.g. [a.go:10-20] or [a.go:1-5; b.go:7]. Valid
// references (clamped to the evidence range) become links to where link
// points, the lines at the answer's commit on the git server; anything else
// in a citation group is removed, so an answer can only cite code it was
// given. Brackets that are not citations are left alone. A bare reference
// outside brackets, a.go:10-20, is linked too when it is valid, and else left
// as written; one in a code block or inside an inline code span, where a link
// would not render, is listed as a link after the text.
func LinkCitations(text string, used []retrieval.Hit, link func(Citation) string) (string, []Citation) {
	cites := []Citation{}
	seen := map[Citation]bool{}
	cite := func(c Citation) string {
		if !seen[c] {
			seen[c] = true
			cites = append(cites, c)
		}
		return Link(c, link(c))
	}
	fences := fenceRe.FindAllStringIndex(text, -1)
	prose := []byte(text) // the code blocks blanked, so that their backticks pair with none outside
	for _, f := range fences {
		for i := f[0]; i < f[1]; i++ {
			prose[i] = ' '
		}
	}
	spans := inlineRe.FindAllIndex(prose, -1)
	var b strings.Builder
	var coded []Citation
	last := 0
	for _, m := range citeRe.FindAllStringSubmatchIndex(text, -1) {
		group, bare := m[2:4], m[6] >= 0
		if bare {
			group = m[6:8]
		}
		refs, ok := parseRefs(text[group[0]:group[1]])
		if !ok {
			continue // ordinary brackets or a markdown link: keep as written
		}
		var shown []Citation
		for _, r := range refs {
			if c, ok := verify(r, used); ok {
				shown = append(shown, c)
			}
		}
		if inside(fences, m[0]) || text[m[0]] != '`' && inside(spans, m[0]) { // in code, but for a whole span of it
			coded = append(coded, shown...)
			continue
		} else if bare && (len(shown) == 0 || m[0] > 0 && text[m[0]-1] == '\\') {
			continue // no source shown, or one of a Windows path: perhaps no reference at all, as in host:8080
		}
		b.WriteString(text[last:m[0]])
		last = m[1]
		links := make([]string, len(shown))
		for i, c := range shown {
			links[i] = cite(c)
		}
		b.WriteString(strings.Join(links, " "))
	}
	b.WriteString(text[last:])
	var links []string
	for _, c := range coded {
		if !seen[c] {
			links = append(links, cite(c))
		}
	}
	if len(links) > 0 {
		if strings.Count(text, "```")%2 == 1 {
			b.WriteString("\n```") // close the code block left open, or the links land in it
		}
		b.WriteString("\n\n來源：" + strings.Join(links, " "))
	}
	return b.String(), cites
}

// Pathless reports whether text, outside code, cites lines without naming
// their file, [55-64] or [5:1-29], which LinkCitations leaves as written.
func Pathless(text string) bool {
	return pathlessRe.MatchString(inlineRe.ReplaceAllString(fenceRe.ReplaceAllString(text, ""), ""))
}

// Link is the Markdown link LinkCitations writes for citation c, to url.
func Link(c Citation, url string) string {
	return fmt.Sprintf("[%s:%d-%d](%s)", linkText.Replace(c.Path), c.StartLine, c.EndLine, url)
}

func inside(spans [][]int, i int) bool {
	for _, s := range spans {
		if i >= s[0] && i < s[1] {
			return true
		}
	}
	return false
}

// parseRefs reads the inside of a bracket group. ok is false unless every part
// is a file reference, so arr[i:2] or [see below] are not citations.
func parseRefs(group string) ([]ref, bool) {
	var refs []ref
	for _, part := range strings.FieldsFunc(group, func(r rune) bool { return r == ',' || r == ';' }) {
		part = labelRe.ReplaceAllString(strings.TrimSpace(strings.ReplaceAll(part, `\_`, "_")), "") // Markdown-escaped underscores
		if len(refs) > 0 && rangeRe.MatchString(part) {
			part = refs[len(refs)-1].path + ":" + part
		}
		m := refRe.FindStringSubmatch(part)
		if m == nil {
			return nil, false
		}
		path := strings.TrimPrefix(strings.TrimPrefix(m[1], "./"), "/")
		if !strings.ContainsAny(path, "./") {
			return nil, false
		}
		start, _ := strconv.Atoi(m[2])
		end := start
		if m[3] != "" {
			end, _ = strconv.Atoi(m[3])
		}
		refs = append(refs, ref{path, min(start, end), max(start, end)})
	}
	return refs, len(refs) > 0
}

// verify accepts a reference that overlaps a shown source of the same file and
// clamps it to that source.
func verify(r ref, used []retrieval.Hit) (Citation, bool) {
	match := r.path
	if !slices.ContainsFunc(used, func(h retrieval.Hit) bool { return h.Path == r.path }) {
		// Models sometimes cite by file name alone; accept it when it is unambiguous.
		match = ""
		for _, h := range used {
			if strings.HasSuffix(h.Path, "/"+r.path) {
				if match != "" && match != h.Path {
					return Citation{}, false
				}
				match = h.Path
			}
		}
		// Or with folders put in front, src/lib/express.js for lib/express.js:
		// then it is the longest shown path it ends with (one the loop above
		// found is longer still, so stays), in a folder itself, as a root
		// main.go is no guess for a cited cmd/foo/main.go.
		for _, h := range used {
			if strings.HasSuffix(r.path, "/"+h.Path) && len(h.Path) > len(match) && strings.Contains(h.Path, "/") {
				match = h.Path
			}
		}
	}
	for _, h := range used {
		if h.Path == match && r.start <= h.EndLine && r.end >= h.StartLine {
			return Citation{Path: match, StartLine: max(r.start, h.StartLine), EndLine: min(r.end, h.EndLine)}, true
		}
	}
	return Citation{}, false
}
