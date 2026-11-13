package contract

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/platform/ai"
	aicontract "deepcode/internal/platform/ai/contract"
	"deepcode/internal/repo/index/analyzer"
)

// Violation is one way a reply breaks its request's rules.
type Violation = aicontract.Violation

// Ask is how a request is sent and its reply read (aicontract.JSON).
type Ask[T any] func(context.Context, ai.Prompt) (T, string, error)

// Name is unit key k as a title, the residual for one the reply lacks: its
// folder's or guide's name, the kind's for an aside, joined for a merged one.
func Name(k string) string {
	switch k {
	case "./":
		return "Root folder"
	case analyzer.Docs:
		return "Documentation"
	case analyzer.Examples:
		return "Examples"
	case analyzer.Tests:
		return "Tests"
	case analyzer.Tooling:
		return "Build and tooling"
	}
	var names []string
	for _, part := range strings.Split(k, ", ") {
		base := path.Base(strings.TrimSuffix(part, "/"))
		if dir, ok := strings.CutSuffix(part, place.TestsMark); ok { // a folder's tests
			base = Name(dir) + " tests"
		} else if part == "./" { // merged with another unit
			base = Name(part)
		} else if !strings.HasSuffix(part, "/") {
			base = strings.TrimSuffix(base, path.Ext(base))
		}
		names = append(names, base)
	}
	return strings.Join(names, ", ")
}

// title is s as a page's title: on one line, clipped.
func title(s string) string { return outline.Clip(outline.OneLine(s), outline.MaxTitle) }

// Page is h as a page at the top, titled and its terms clipped.
func (h Head) Page() outline.Page {
	return outline.Page{Title: title(h.Title), Terms: outline.Terms(h.Terms), Parent: -1}
}

// Clean is l, each label on one line and clipped.
func (l Labels) Clean() Labels {
	for _, e := range l.each() {
		*e.at = title(*e.at)
	}
	return l
}

// Note asks for the repair, naming each violation code does not fix alone.
func Note(vs []Violation) string {
	var b strings.Builder
	b.WriteString("Your reply breaks these rules of the request. Reply again with the whole JSON, these fixed and the rest as it was:\n")
	for _, v := range vs {
		if !v.Minor {
			fmt.Fprintf(&b, "- %s\n", v.Detail)
		}
	}
	return b.String()
}

// terms checks a page's terms: left out (null), more than outline.MaxTerms,
// which are clipped, or one blank (a null term reads as ""), which goes
// (outline.Terms); an empty list is terms given.
func terms(where string, ts []string) []Violation {
	switch {
	case ts == nil:
		return []Violation{{Rule: "terms.missing", Where: where, Detail: where + ": terms is missing", Minor: true}}
	case len(ts) > outline.MaxTerms:
		return []Violation{{Rule: "terms.over", Where: where, Detail: fmt.Sprintf("%s: %d terms, at most %d", where, len(ts), outline.MaxTerms), Minor: true}}
	case slices.ContainsFunc(ts, func(t string) bool { return outline.OneLine(t) == "" }):
		return []Violation{{Rule: "terms.blank", Where: where, Detail: where + ": a term is blank", Minor: true}}
	}
	return nil
}

// unique is t, or, when seen holds it already, t numbered, "t (2)", "t (3)"…,
// clipped to make room for the number; seen then holds it.
func unique(t string, seen map[string]bool) string {
	base := t
	for n := 2; seen[t]; n++ {
		suffix := fmt.Sprintf(" (%d)", n)
		t = outline.Clip(base, outline.MaxTitle-len(suffix)) + suffix
	}
	seen[t] = true
	return t
}

// Distinct numbers each title pages repeat (unique) and reports how many it
// renamed: a section planned in several requests (Split), each checked alone,
// is checked across them here.
func Distinct(pages []outline.Page) int {
	seen, n := map[string]bool{}, 0
	for i := range pages {
		if t := unique(pages[i].Title, seen); t != pages[i].Title {
			pages[i].Title, n = t, n+1
		}
	}
	return n
}

// Outline is the outline's request held to its rules: a head for each of
// keys, titled; the labels. A key left out, or untitled, is titled
// names[key]; an empty label is English's.
func Outline(ask Ask[OutlineReply], keys []string, names map[string]string) aicontract.Turn[OutlineReply] {
	return aicontract.Turn[OutlineReply]{Kind: "outline", Ask: ask, Note: Note,
		Check: func(r OutlineReply) []Violation { return checkOutline(r.Overview, r.Labels, r.Pages, keys) },
		Fix: func(r *OutlineReply) {
			r.Overview, r.Labels = fixHeads(r.Overview, r.Labels, &r.Pages, keys, names)
		}}
}

// checkOutline checks an overview, labels and the heads of keys.
func checkOutline(overview Head, l Labels, pages map[string]Head, keys []string) []Violation {
	var vs []Violation
	if title(overview.Title) == "" {
		vs = append(vs, Violation{Rule: "outline.title", Where: "overview", Detail: "overview has an empty title"})
	}
	vs = append(vs, terms("overview", overview.Terms)...)
	for _, k := range keys {
		h, ok := pages[k]
		switch {
		case !ok:
			vs = append(vs, Violation{Rule: "outline.missing", Where: k, Detail: fmt.Sprintf("pages.%q is missing: give its title and terms", k)})
			continue
		case title(h.Title) == "":
			vs = append(vs, Violation{Rule: "outline.title", Where: k, Detail: fmt.Sprintf("pages.%q has an empty title", k)})
		}
		vs = append(vs, terms(k, h.Terms)...)
	}
	if slices.ContainsFunc(l.each(), func(e label) bool { return strings.TrimSpace(*e.at) == "" }) {
		vs = append(vs, Violation{Rule: "outline.label", Where: "labels", Detail: "labels.stack, labels.otherFiles, labels.architecture and labels.structure must each hold a heading"})
	}
	return vs
}

// fixHeads titles what checkOutline found untitled, and fills empty labels.
func fixHeads(overview Head, l Labels, pages *map[string]Head, keys []string, names map[string]string) (Head, Labels) {
	if title(overview.Title) == "" {
		overview.Title = "Overview"
	}
	if *pages == nil {
		*pages = map[string]Head{}
	}
	for _, k := range keys {
		if h := (*pages)[k]; title(h.Title) == "" {
			(*pages)[k] = Head{Title: names[k], Terms: h.Terms}
		}
	}
	for _, e := range l.each() {
		if strings.TrimSpace(*e.at) == "" {
			*e.at = e.en
		}
	}
	return overview, l
}

// Section is a section's request held to its rules, over its units: each
// unit 1 to its cap pages, titled, of 1 to maxFiles of its files, no two
// titled alike.
func Section(ask Ask[SectionReply], units []place.Unit) aicontract.Turn[SectionReply] {
	return aicontract.Turn[SectionReply]{Kind: "section", Ask: ask, Note: Note,
		Check: func(r SectionReply) []Violation { return checkUnits("section", r, units) },
		Fix:   func(r *SectionReply) { fixUnits(r, units) }}
}

// checkUnits checks each unit's pages in r, its rules named under prefix.
func checkUnits(prefix string, r SectionReply, units []place.Unit) []Violation {
	var vs []Violation
	seen := map[string]bool{}
	for _, u := range units {
		pages := r[u.Key]
		if len(pages) == 0 {
			vs = append(vs, Violation{Rule: aicontract.Rule(prefix + ".missing"), Where: u.Key, Detail: fmt.Sprintf("%q: no page; give it at least one", u.Key)})
		}
		if len(pages) > u.Cap {
			vs = append(vs, Violation{Rule: aicontract.Rule(prefix + ".over"), Where: u.Key, Detail: fmt.Sprintf("%q: %d pages, at most %d", u.Key, len(pages), u.Cap)})
		}
		for _, p := range pages {
			t := title(p.Title)
			switch {
			case t == "":
				vs = append(vs, Violation{Rule: aicontract.Rule(prefix + ".title"), Where: u.Key, Detail: fmt.Sprintf("a page of %q has an empty title", u.Key)})
			case seen[t]:
				vs = append(vs, Violation{Rule: aicontract.Rule(prefix + ".duplicate"), Where: t, Detail: fmt.Sprintf("two pages are titled %q", t)})
			}
			seen[t] = true
			for _, f := range p.Files {
				if !slices.Contains(u.Code, f) {
					vs = append(vs, Violation{Rule: aicontract.Rule(prefix + ".file"), Where: f, Detail: fmt.Sprintf("page %q: %s is not a file of %q", t, f, u.Key)})
				}
			}
			if len(p.Files) == 0 || len(p.Files) > maxFiles { // too many is Minor: code keeps the first
				vs = append(vs, Violation{Rule: aicontract.Rule(prefix + ".files"), Where: t, Detail: fmt.Sprintf("page %q: %d files, 1 to %d", t, len(p.Files), maxFiles), Minor: len(p.Files) > 0})
			}
			vs = append(vs, terms(t, p.Terms)...)
		}
	}
	return vs
}

// fixUnits is code's fix for what checkUnits finds: a unit with no page gets
// one titled by its name (placement gives it the unit's files); a file not of
// the unit goes; a page keeps its first maxFiles, and one left with none goes,
// unless no page of its unit has any: then its first pages up to its files
// (Code, never none) stay, which placement fills, a file a page at least;
// then pages past the cap go (their files back to placement); an empty title
// is the unit's name, and a title taken already is numbered (unique).
func fixUnits(r *SectionReply, units []place.Unit) {
	if *r == nil {
		*r = SectionReply{}
	}
	seen := map[string]bool{}
	empty := func(p Planned) bool { return len(p.Files) == 0 }
	for _, u := range units {
		pages := slices.Clone((*r)[u.Key])
		if len(pages) == 0 {
			pages = []Planned{{Title: Name(u.Key)}}
		}
		for i, p := range pages {
			var files []string
			for _, f := range p.Files {
				if slices.Contains(u.Code, f) && !slices.Contains(files, f) && len(files) < maxFiles {
					files = append(files, f)
				}
			}
			pages[i].Files = files
		}
		if slices.ContainsFunc(pages, func(p Planned) bool { return !empty(p) }) {
			pages = slices.DeleteFunc(pages, empty) // before the cap, so an empty page takes no place of one with files
		} else {
			pages = pages[:max(1, min(len(pages), len(u.Code)))]
		}
		pages = pages[:min(len(pages), u.Cap)]
		for i, p := range pages {
			t := title(p.Title)
			if t == "" {
				t = Name(u.Key)
			}
			pages[i] = Planned{Title: unique(t, seen), Terms: p.Terms, Files: p.Files}
		}
		(*r)[u.Key] = pages
	}
}

// Flat is a flat wiki's request held to its rules: the outline's for the
// overview and labels, a section's (as flat.*) for every unit, code and
// aside kinds alike.
func Flat(ask Ask[FlatReply], units []place.Unit) aicontract.Turn[FlatReply] {
	return aicontract.Turn[FlatReply]{Kind: "flat", Ask: ask, Note: Note,
		Check: func(r FlatReply) []Violation {
			return append(checkOutline(r.Overview, r.Labels, nil, nil), checkUnits("flat", r.Units, units)...)
		},
		Fix: func(r *FlatReply) {
			r.Overview, r.Labels = fixHeads(r.Overview, r.Labels, &map[string]Head{}, nil, nil)
			fixUnits(&r.Units, units)
		}}
}

// Pages are r's pages of units, in order, each under its unit.
func Pages(r SectionReply, units []place.Unit) []outline.Page {
	var out []outline.Page
	for _, u := range units {
		for _, p := range r[u.Key] {
			var files []string
			for _, f := range p.Files {
				if !slices.Contains(files, f) {
					files = append(files, f)
				}
			}
			out = append(out, outline.Page{Title: title(p.Title), Terms: outline.Terms(p.Terms), Chosen: files, Units: []string{u.Key}, Parent: -1})
		}
	}
	return out
}
