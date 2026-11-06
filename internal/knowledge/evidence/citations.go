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
	// groupRe matches one bracket group, optionally already turned into a
	// markdown link by the model (whose target is discarded).
	groupRe = regexp.MustCompile(`\[([^\[\]\n]+)\](\([^)\s]*\))?`)
	// refRe matches one reference inside a group: path:10-20, path:10 or
	// path:L10-L20, optionally in backticks.
	refRe = regexp.MustCompile("^`?([^\\s`]+?):L?(\\d+)(?:-L?(\\d+))?`?$")
	// rangeRe matches a bare range that continues the previous reference's
	// file, like the 99-100 in [a.go:43-51, 99-100].
	rangeRe = regexp.MustCompile(`^L?\d+(?:-L?\d+)?$`)
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
// given. Brackets that are not citations are left alone.
func LinkCitations(text string, used []retrieval.Hit, link func(Citation) string) (string, []Citation) {
	cites := []Citation{}
	seen := map[Citation]bool{}
	fences := fenceRe.FindAllStringIndex(text, -1)
	var b strings.Builder
	last := 0
	for _, m := range groupRe.FindAllStringSubmatchIndex(text, -1) {
		if inside(fences, m[0]) {
			continue // code, where a link would not render
		}
		refs, ok := parseRefs(text[m[2]:m[3]])
		if !ok {
			continue // ordinary brackets or a markdown link: keep as written
		}
		b.WriteString(text[last:m[0]])
		last = m[1]
		var links []string
		for _, r := range refs {
			c, ok := verify(r, used)
			if !ok {
				continue
			}
			links = append(links, fmt.Sprintf("[%s:%d-%d](%s)", linkText.Replace(c.Path), c.StartLine, c.EndLine, link(c)))
			if !seen[c] {
				seen[c] = true
				cites = append(cites, c)
			}
		}
		b.WriteString(strings.Join(links, " "))
	}
	b.WriteString(text[last:])
	return b.String(), cites
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
		part = strings.TrimSpace(strings.ReplaceAll(part, `\_`, "_")) // Markdown-escaped underscores
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
	}
	for _, h := range used {
		if h.Path == match && r.start <= h.EndLine && r.end >= h.StartLine {
			return Citation{Path: match, StartLine: max(r.start, h.StartLine), EndLine: min(r.end, h.EndLine)}, true
		}
	}
	return Citation{}, false
}
