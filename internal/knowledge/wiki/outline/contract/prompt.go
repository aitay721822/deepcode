package contract

import (
	"fmt"
	"slices"
	"strings"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/repo/index/analyzer"
)

// The planning requests' instructions, the same on every request of a kind,
// so the model server's prompt cache holds them; what varies follows in the
// user's message. None states a number of pages: a unit's bounds are its
// schema's, held by the checks and code; with types only (past the limits),
// the checks' and code's alone.
const (
	OutlinePrompt = `You plan the outline of a technical wiki for a code repository. Reply with the JSON the schema asks for.
"overview" is the page introducing the whole repository. "labels" are two headings written in the wiki's language: "stack" says "Technology stack" and "otherFiles" says "Other files".
"pages" holds an entry for each key listed below, a section of the wiki or a page at its top: a title naming what that part of the code does, not just its folder, and search terms: identifiers, file names and concepts from it, the most telling first.`

	SectionPrompt = `You plan the pages of one section of a technical wiki for a code repository. Reply with the JSON the schema asks for: for each unit key it names, the pages planned under that unit, each with a title, search terms (identifiers, file names and concepts, the most telling first) and the files it is about, chosen from that unit's files as listed.
A page covers one subsystem, workflow or feature a reader looks up, named plainly in its title; small files serving one purpose share a page. No two pages share a subject, and none repeats another section's.`

	AsidePrompt = `You plan the pages of one section of a technical wiki for a code repository, a section of its tests, examples or guides. Reply with the JSON the schema asks for: the section's pages, each with a title and search terms, and under "assign" the page each listed unit goes to, by its index in "pages", the first being 0.
Group the units by the feature, workflow or tool they serve: a page covers what a reader looks up, and gives a unit a page of its own only when it is a subject of its own.`

	FlatPrompt = `You plan a concise technical wiki for a small code repository. Reply with the JSON the schema asks for.
"overview" is the page introducing the whole repository. "labels" are two headings written in the wiki's language: "stack" says "Technology stack" and "otherFiles" says "Other files".
"units" holds, for each unit key listed, the pages planned under that unit, each with a title, search terms (identifiers, file names and concepts, the most telling first) and the files it is about, chosen from that unit's files as listed. A page covers one subsystem, workflow or feature a reader looks up, named plainly in its title; small files serving one purpose share a page, and no two pages share a subject.`
)

// Part is a key of the outline: a section of units, a top-level page of one,
// or an aside kind's section.
type Part struct {
	Key   string
	Units []place.Unit
	Kind  string // the aside kind, "" for code
}

// what names an aside kind's files for the model.
var what = map[string]string{analyzer.Docs: "documentation and guides", analyzer.Examples: "examples",
	analyzer.Tests: "tests and benchmarks", analyzer.Tooling: "build, packaging, CI and editor configuration"}

// OutlineMessage is the outline request's message: what all of a wiki's
// requests share first (about), the layout, then each part with some files.
func OutlineMessage(about, layout string, parts []Part, lines map[string]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\nThe wiki's parts, by key, with some of their files:\n", about, layout)
	for _, p := range parts {
		var keys, files []string
		for _, u := range p.Units {
			keys, files = append(keys, u.Key), append(files, u.Files...)
		}
		name := strings.Join(keys, ", ")
		if p.Kind != "" {
			name = what[p.Kind]
		}
		fmt.Fprintf(&b, "%s. %s (%d files): %s\n", p.Key, name, len(files), strings.Join(outline.Pick(files, lines, 12), ", "))
	}
	return b.String()
}

// SectionMessage is a section request's message: what all of a wiki's
// section requests share first (about and the sections' titles), the
// section's title, every unit of it with its files and what they declare
// (outline.Pick, 200 over the section), and, for a request of some of its
// units only, which.
func SectionMessage(about string, titles []string, title string, section, asked []place.Unit, declared map[string][]string, lines map[string]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sThe wiki's sections: %s\n\nSection: %s\n", about, strings.Join(titles, " | "), title)
	list(&b, section, asked, declared, lines)
	return b.String()
}

// list writes each unit of section with its files and what they declare
// (outline.Pick, 200 over the section), and, when asked is some of them
// only, which to plan.
func list(b *strings.Builder, section, asked []place.Unit, declared map[string][]string, lines map[string]int) {
	var all []string
	for _, u := range section {
		all = append(all, u.Code...)
	}
	shown := map[string]bool{}
	for _, f := range outline.Pick(all, lines, 200) {
		shown[f] = true
	}
	for _, u := range section {
		fmt.Fprintf(b, "\nUnit %q (%d files, %d lines), its files with what they declare:\n", u.Key, len(u.Files), total(u.Files, lines))
		for _, f := range u.Code {
			if shown[f] {
				fmt.Fprintf(b, "%s %s\n", f, strings.Join(first(declared[f], f), ", "))
			}
		}
	}
	if len(asked) < len(section) {
		var keys []string
		for _, u := range asked {
			keys = append(keys, fmt.Sprintf("%q", u.Key))
		}
		fmt.Fprintf(b, "\nPlan now the pages of %s only; the other units are listed so that no two pages share a subject.\n", strings.Join(keys, ", "))
	}
}

// AsideMessage is an aside request's message: about and the sections'
// titles first, then the section and each unit with what it holds; a group
// (aside.Group) with how many units it joins and as many of their keys as
// its share of a budget of names allows (groups at most 100, ten a group at
// least), spread over them, never their headings, so a group's line stays
// short however many it joins; then, with a group, that it goes to one page;
// then the groups planned apart (aside.Apart), so that no page here takes
// their subjects.
func AsideMessage(about string, titles []string, title, kind string, units, apart []place.Unit, declared map[string][]string, lines map[string]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sThe wiki's sections: %s\n\nSection: %s, the repository's %s. Its units:\n", about, strings.Join(titles, " | "), title, what[kind])
	groups := len(slices.DeleteFunc(slices.Concat(units, apart), func(u place.Unit) bool { return !grouped(u) }))
	group := func(u place.Unit) {
		most := names / groups
		shown := u.Raw
		if len(u.Raw) > most { // spread over them, not the first by name
			shown = make([]string, most, most+1)
			for i := range shown {
				shown[i] = u.Raw[i*len(u.Raw)/most]
			}
			shown = append(shown, "…")
		}
		fmt.Fprintf(&b, "- %q (a group of %d units, %d files, %d lines): %s\n", u.Key, len(u.Raw), len(u.Files), total(u.Files, lines), strings.Join(shown, ", "))
	}
	for _, u := range units {
		if grouped(u) {
			group(u)
			continue
		}
		held := outline.Pick(u.Files, lines, 6)
		if kind == analyzer.Docs && slices.Contains(u.Files, u.Key) && (len(u.Files) == 1 || len(declared[u.Key]) > 0) { // a guide by its headings, not the code it shows
			held = first(declared[u.Key], u.Key)
		}
		fmt.Fprintf(&b, "- %q (%d files, %d lines): %s\n", u.Key, len(u.Files), total(u.Files, lines), strings.Join(held, ", "))
	}
	if groups > len(apart) {
		b.WriteString("\nA group stands for the units it joins, and goes to one page as a unit does.\n")
	}
	if len(apart) > 0 {
		b.WriteString("\nThese are planned apart, in pages of their own; they are listed so that no page here takes their subjects, and are assigned none:\n")
		for _, u := range apart {
			group(u)
		}
	}
	return b.String()
}

// names is the most units the groups' lines of a message name.
const names = 1000

// FlatMessage is a flat wiki's message: about, the layout, then each unit
// with its files and what they declare.
func FlatMessage(about, layout string, units []place.Unit, declared map[string][]string, lines map[string]int) string {
	var b strings.Builder
	b.WriteString(about + layout + "\n")
	list(&b, units, units, declared, lines)
	return b.String()
}

// first is the names a file declares as a message shows them: a guide's
// first 24 sections, any other file's first 6.
func first(names []string, f string) []string {
	most := 6
	if lang, _ := analyzer.Detect(f); lang == "markdown" {
		most = 24
	}
	out := slices.Clone(names[:min(len(names), most)])
	for i, n := range out {
		out[i] = outline.Clip(n, 60)
	}
	return out
}

// total is files' lines.
func total(files []string, lines map[string]int) int {
	n := 0
	for _, f := range files {
		n += lines[f]
	}
	return n
}
