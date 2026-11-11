package skeleton

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	aicontract "deepcode/internal/platform/ai/contract"
)

// Draft is a page's text as code holds it: the paragraph it opens with and
// its sections in order.
type Draft struct {
	Intro    string
	Sections []Section
}

// Section is a heading of a draft and the text under it.
type Section struct {
	Name  string
	Level int
	Body  string
}

// The rules a page's text is held to: each heading of its skeleton is there,
// with text, and each section with text cites a line that checks out.
const (
	Missing aicontract.Rule = "write.heading"
	Empty   aicontract.Rule = "write.empty"
	Uncited aicontract.Rule = "write.uncited"
)

// find is the index of the heading named name, case and wrapping aside; -1
// for none.
func (s Skeleton) find(name string) int {
	return slices.IndexFunc(s, func(h Heading) bool { return fold(h.Name) == fold(name) })
}

// find is the index of the section headed name, case and wrapping aside; -1
// for none.
func (d Draft) find(name string) int {
	return slices.IndexFunc(d.Sections, func(x Section) bool { return fold(x.Name) == fold(name) })
}

// fenceRe matches a code block's fence line: its mark ("```", "~~~~") and
// what follows it on the line ("go").
var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})([^\n]*)")

// fence is fenceRe's match of line, nil when it is no fence: a backtick
// fence's info string holds no backtick, as in CommonMark.
func fence(line string) []string {
	if m := fenceRe.FindStringSubmatch(line); m != nil && !(m[1][0] == '`' && strings.Contains(m[2], "`")) {
		return m
	}
	return nil
}

// empty reports whether section i of d has no text, nor, under a level-2
// heading, do the level-3 sections that follow it: a guide's heading may open
// straight onto its sections.
func (d Draft) empty(i int) bool {
	for j := i; j < len(d.Sections) && (j == i || d.Sections[i].Level == 2 && d.Sections[j].Level == 3); j++ {
		if d.Sections[j].Body != "" {
			return false
		}
	}
	return true
}

// Parse reads Markdown md as a draft of s: the text before its first
// "##" or "###" heading outside code blocks is the intro, and each such
// heading and the text up to the next is a section. A heading s holds, case
// and wrapping aside, takes s's name and level.
func Parse(md string, s Skeleton) Draft {
	var d Draft
	var body strings.Builder
	flush := func() {
		if text := strings.TrimSpace(body.String()); len(d.Sections) == 0 {
			d.Intro = text
		} else {
			d.Sections[len(d.Sections)-1].Body = text
		}
		body.Reset()
	}
	mark := "" // the open block's
	for line := range strings.Lines(md) {
		m := fence(line)
		switch {
		case m != nil && mark == "":
			mark = m[1]
		case m != nil && m[1][0] == mark[0] && len(m[1]) >= len(mark) && strings.TrimSpace(m[2]) == "":
			mark = ""
		case mark == "" && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ")):
			flush()
			level := 2
			if strings.HasPrefix(line, "### ") {
				level = 3
			}
			name := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if i := s.find(name); i >= 0 {
				name, level = s[i].Name, s[i].Level
			}
			d.Sections = append(d.Sections, Section{Name: name, Level: level})
			continue
		}
		body.WriteString(line)
	}
	flush()
	return d
}

// Render is d as Markdown: its intro, then each section under its heading.
func Render(d Draft) string {
	var parts []string
	if d.Intro != "" {
		parts = append(parts, d.Intro)
	}
	for _, x := range d.Sections {
		parts = append(parts, strings.TrimSpace(strings.Repeat("#", x.Level)+" "+x.Name+"\n\n"+x.Body))
	}
	return strings.Join(parts, "\n\n")
}

// Check names what d breaks of s: a heading of s missing or with no text
// (empty), and any section, s's or one the model added, whose text cites nothing that
// checks out (cited counts what does), unless the text is only a diagram;
// the intro needs none. With no skeleton, a page citing nothing at all is
// named whole, Where "".
func Check(s Skeleton, cited func(string) int) func(Draft) []aicontract.Violation {
	uncited := func(body string) bool { return body != "" && !diagram(body) && cited(body) == 0 }
	return func(d Draft) []aicontract.Violation {
		if len(s) == 0 && cited(Render(d)) == 0 {
			return []aicontract.Violation{{Rule: Uncited, Detail: "the page cites nothing that can be checked"}}
		}
		var out []aicontract.Violation
		for _, h := range s {
			switch i := d.find(h.Name); {
			case i < 0:
				out = append(out, aicontract.Violation{Rule: Missing, Where: h.Name, Detail: "is missing"})
			case d.empty(i):
				out = append(out, aicontract.Violation{Rule: Empty, Where: h.Name, Detail: "has no text"})
			case uncited(d.Sections[i].Body):
				out = append(out, aicontract.Violation{Rule: Uncited, Where: h.Name, Detail: "cites nothing that can be checked"})
			}
		}
		for _, x := range d.Sections {
			if s.find(x.Name) < 0 && uncited(x.Body) {
				out = append(out, aicontract.Violation{Rule: Uncited, Where: x.Name, Detail: "cites nothing that can be checked"})
			}
		}
		return out
	}
}

// diagram reports whether body is only a fenced mermaid block.
func diagram(body string) bool {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	open, end := fence(lines[0]), fence(lines[len(lines)-1])
	return len(lines) > 1 && open != nil && end != nil && strings.HasPrefix(strings.TrimSpace(open[2]), "mermaid") &&
		end[1][0] == open[1][0] && len(end[1]) >= len(open[1]) && strings.TrimSpace(end[2]) == ""
}

// Merge joins fix, the repair of the sections vs name, into first: each
// one fix holds replaces first's, or goes where s puts it, after the last
// section of a heading s puts before it; one the model added takes the
// repair's text where it is. A repair of the page (Where "") replaces it.
func Merge(s Skeleton) func(first, fix Draft, vs []aicontract.Violation) Draft {
	return func(first, fix Draft, vs []aicontract.Violation) Draft {
		if slices.ContainsFunc(vs, func(v aicontract.Violation) bool { return v.Where == "" }) {
			return fix
		}
		out := first
		out.Sections = slices.Clone(first.Sections)
		for _, v := range vs {
			j, k := fix.find(v.Where), s.find(v.Where)
			if j < 0 {
				continue
			}
			if k < 0 {
				if i := out.find(v.Where); i >= 0 {
					out.Sections[i].Body = fix.Sections[j].Body
				}
				continue
			}
			x := Section{Name: s[k].Name, Level: s[k].Level, Body: fix.Sections[j].Body}
			if i := out.find(v.Where); i >= 0 {
				out.Sections[i] = x
				continue
			}
			at := 0
			for i, y := range out.Sections {
				if m := s.find(y.Name); m >= 0 && m < k {
					at = i + 1
				}
			}
			out.Sections = slices.Insert(out.Sections, at, x)
		}
		return out
	}
}

// Fix is code's fix of draft d: a heading with no text (empty) goes, the
// skeleton's or the model's, as code writes no text for the model and a bare
// heading says nothing. A missing heading of the skeleton stays missing, an
// uncited one stays, both counted.
func Fix(d *Draft) {
	var kept []Section
	for i, x := range d.Sections {
		if !d.empty(i) {
			kept = append(kept, x)
		}
	}
	d.Sections = kept
}

// Lacking are the headings of s that d does not hold.
func Lacking(s Skeleton, d Draft) []string {
	var out []string
	for _, h := range s {
		if d.find(h.Name) < 0 {
			out = append(out, h.Name)
		}
	}
	return out
}

// Gained are the headings of is that was does not give, case and wrapping
// aside: what a change of the code a page's plan holds brings it.
func Gained(was, is Skeleton) Skeleton {
	return slices.DeleteFunc(slices.Clone(is), func(h Heading) bool { return was.find(h.Name) >= 0 })
}

// Note is the repair's message for the violations of a page of s: what each
// named section lacks, asking for those sections only, or for the page again
// when it cites nothing; cite is a range the sources show, as an example.
func Note(s Skeleton, cite string) func([]aicontract.Violation) string {
	return func(vs []aicontract.Violation) string {
		if slices.ContainsFunc(vs, func(v aicontract.Violation) bool { return v.Where == "" }) {
			return fmt.Sprintf("Your page has no citation that can be checked. Write the whole page again, citing after each statement the lines it rests on as [path:start-end], with the path and lines a <source> block shows, as in %s.", cite)
		}
		var lines, heads []string
		for _, v := range vs {
			line := fmt.Sprintf("- %q %s", v.Where, v.Detail)
			if v.Rule == Uncited {
				line += fmt.Sprintf(": cite after each statement the lines it rests on as [path:start-end], with the path and lines a <source> block shows, as in %s", cite)
			}
			level := 2
			if k := s.find(v.Where); k >= 0 {
				level = s[k].Level
			}
			lines, heads = append(lines, line+"."), append(heads, strings.Repeat("#", level)+" "+v.Where)
		}
		return "Some sections of your page need repair:\n" + strings.Join(lines, "\n") +
			"\n\nWrite only these sections again, each as a Markdown block under its heading exactly as given here, nothing else:\n" + strings.Join(heads, "\n")
	}
}
