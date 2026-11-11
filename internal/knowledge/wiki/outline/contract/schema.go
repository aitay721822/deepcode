// Package contract holds the model's planning replies to their requests'
// rules: the outline (the overview, labels and a title per section and
// top-level page), a section's pages per unit, an aside's pages and the page
// each unit goes to, and a flat wiki's in one. Each request's schema states
// the rules; each check names what a reply breaks, each note asks for the
// repair, and each fix is code's for what is left (ai/contract.Run).
package contract

import (
	"fmt"
	"slices"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/platform/ai"
)

// maxFiles is the most files a page's reply names.
const maxFiles = 16

// Bounds is whether a schema carries the bounds (enums, item counts,
// integer ranges) as well as the types. Requests carry them (Probe C chose
// that over types alone), unless the schema with them is past the
// structured-output limits.
type Bounds bool

// Head is a page with no files of its own: its title and search terms.
type Head struct {
	Title string   `json:"title"`
	Terms []string `json:"terms"`
}

// Planned is a page planned under a unit: its title, terms, and the files
// it is about, of its unit's.
type Planned struct {
	Title string   `json:"title"`
	Terms []string `json:"terms"`
	Files []string `json:"files"`
}

// Labels are the fixed headings in the wiki's language.
type Labels struct {
	Stack      string `json:"stack"`
	OtherFiles string `json:"otherFiles"`
}

// English labels, the residual for an empty one.
var English = Labels{Stack: "Technology stack", OtherFiles: "Other files"}

// The replies: the outline's, by key ("1", "p1"); a section's, by unit key;
// an aside's pages and each unit's page; a flat wiki's.
type (
	OutlineReply struct {
		Overview Head            `json:"overview"`
		Labels   Labels          `json:"labels"`
		Pages    map[string]Head `json:"pages"`
	}
	SectionReply map[string][]Planned
	AsideReply   struct {
		Pages  []Head          `json:"pages"`
		Assign map[string]*int `json:"assign"` // nil for a null index, which no page has
	}
	FlatReply struct {
		Overview Head         `json:"overview"`
		Labels   Labels       `json:"labels"`
		Units    SectionReply `json:"units"`
	}
)

// object is a strict object schema of props, every one required.
func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
}

// array is an array schema of items, between least and most of them when b.
func array(items map[string]any, least, most int, b Bounds) map[string]any {
	s := map[string]any{"type": "array", "items": items}
	if b && least > 0 {
		s["minItems"] = least
	}
	if b && most > 0 {
		s["maxItems"] = most
	}
	return s
}

var str = map[string]any{"type": "string"}

// head is a Head's schema.
func head(b Bounds) map[string]any {
	return object(map[string]any{"title": str, "terms": array(str, 0, outline.MaxTerms, b)})
}

// planned is a Planned page's schema, its files from choices when b.
func planned(choices []string, b Bounds) map[string]any {
	file := map[string]any{"type": "string"}
	if b {
		file["enum"] = choices
	}
	return object(map[string]any{"title": str, "terms": array(str, 0, outline.MaxTerms, b), "files": array(file, 1, maxFiles, b)})
}

var labels = object(map[string]any{"stack": str, "otherFiles": str})

// OutlineFormat asks for the overview, the labels and a head per key. Its
// bounds hold no enum, so it is past the limits with or without them.
func OutlineFormat(keys []string) (ai.Format[OutlineReply], error) {
	pages := map[string]any{}
	for _, k := range keys {
		pages[k] = head(true)
	}
	return ai.Dynamic[OutlineReply]("wiki_outline", object(map[string]any{"overview": head(true), "labels": labels, "pages": object(pages)}))
}

// units is the schema of each unit's pages: 1 to its cap.
func units(us []place.Unit, b Bounds) map[string]any {
	props := map[string]any{}
	for _, u := range us {
		props[u.Key] = array(planned(u.Code, b), 1, u.Cap, b)
	}
	return object(props)
}

// SectionFormat asks for each of a section's units' pages.
func SectionFormat(us []place.Unit, b Bounds) (ai.Format[SectionReply], error) {
	return ai.Dynamic[SectionReply]("wiki_section", units(us, b))
}

// AsideFormat asks for an aside's pages, 1 to most, and each unit's page. Its
// bounds hold no enum, so it is past the limits with or without them.
func AsideFormat(us []place.Unit, most int) (ai.Format[AsideReply], error) {
	assign := map[string]any{}
	for _, u := range us {
		assign[u.Key] = map[string]any{"type": "integer", "minimum": 0, "maximum": most - 1}
	}
	return ai.Dynamic[AsideReply]("wiki_aside", object(map[string]any{"pages": array(head(true), 1, most, true), "assign": object(assign)}))
}

// FlatFormat asks for the overview, the labels and each unit's pages.
func FlatFormat(us []place.Unit, b Bounds) (ai.Format[FlatReply], error) {
	return ai.Dynamic[FlatReply]("wiki_flat", object(map[string]any{"overview": head(b), "labels": labels, "units": units(us, b)}))
}

// Split is a section's requests within the structured-output limits: all
// its units in one when that fits, else one per unit, a unit whose own
// schema is past them asked with types only. Each request's units, and its
// format, in order.
func Split(us []place.Unit) ([][]place.Unit, []ai.Format[SectionReply], error) {
	if f, err := SectionFormat(us, true); err == nil {
		return [][]place.Unit{us}, []ai.Format[SectionReply]{f}, nil
	}
	var parts [][]place.Unit
	var formats []ai.Format[SectionReply]
	for _, u := range us {
		f, err := SectionFormat([]place.Unit{u}, true)
		if err != nil {
			if f, err = SectionFormat([]place.Unit{u}, false); err != nil {
				return nil, nil, fmt.Errorf("unit %s: %w", u.Key, err)
			}
		}
		parts, formats = append(parts, []place.Unit{u}), append(formats, f)
	}
	return parts, formats, nil
}
