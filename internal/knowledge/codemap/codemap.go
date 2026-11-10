// Package codemap draws a code map for a question, as DeepWiki's Codemap
// mode does: the code paths that answer it as numbered traces, each a run of
// locations (1a, 1b, ...) in the order the code runs or data flows, every
// location one line of code with what happens there, and a guide per trace
// on how its locations work together. The model picks the lines from the
// evidence; each is found again there (ground), and one it made up is dropped.
package codemap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
)

type Map struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Traces      []Trace `json:"traces"`
}

// format asks the model for a Map. It writes no ids: ground numbers the
// traces and locations it keeps.
var format = ai.Schema[Map]("code_map")

type Trace struct {
	ID          string     `json:"id" jsonschema:"-"` // 1, 2, ...
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Guide       string     `json:"guide"` // Markdown: how the locations work together
	Locations   []Location `json:"locations"`
}

type Location struct {
	ID          string `json:"id" jsonschema:"-"` // 1a, 1b, ...
	Title       string `json:"title"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Code        string `json:"code"` // the line as the file reads, trimmed
}

// Request is what a map is drawn from: the conversation so far, a deep
// answer's background ("" for none), the sources evidence.Format made of
// Hits, the question and the language to write in.
type Request struct {
	Repo, Commit        string
	History             []ai.Message
	Background, Sources string
	Question, Language  string
	Hits                []retrieval.Hit // as shown in Sources: where the lines are found
}

// ErrInvalid: the model's map had no location the sources show, twice.
var ErrInvalid = errors.New("the model's code map showed no line of the sources")

// maxEcho bounds how much of a map goes back with the request to try again:
// enough to rework, not a whole runaway reply.
const maxEcho = 8000

// Draw asks model for the map of r's question (format) and grounds it in
// r.Hits. A map whose locations the sources do not show is asked for once
// more; an answer that is no map is an error wrapping ai.ErrSchema. drawing is
// told how much of the replies has come, as it comes. The map has no traces
// only when the model found nothing to map.
func Draw(ctx context.Context, model *ai.OpenAI, r Request, drawing func(chars int)) (Map, error) {
	p, n := prompt(r), 0
	for try := 0; ; try++ {
		m, reply, err := ai.StreamJSON(ctx, model, p, format, func(d string) {
			if n += len(d); drawing != nil {
				drawing(n)
			}
		})
		if err != nil {
			return Map{}, err
		}
		if len(m.Traces) == 0 {
			return m, nil // nothing to map, as the rules let it say
		}
		if ground(&m, r.Hits); len(m.Traces) > 0 {
			return m, nil
		}
		if try == 1 {
			return Map{}, ErrInvalid
		}
		if len(reply) > maxEcho {
			reply = strings.ToValidUTF8(reply[:maxEcho], "") + "…"
		}
		p = p.After(reply, groundNote)
	}
}

// Markdown is the map as text: what the conversation's history, an agent and
// a reader without the map's view get. Each location cites its line with the
// link cite gives it.
func (m Map) Markdown(cite func(Location) string) string {
	one := func(s string) string { return strings.Join(strings.Fields(s), " ") } // a heading or list item is one line
	var b strings.Builder
	if title := one(m.Title); title != "" {
		fmt.Fprintf(&b, "## %s\n\n", title)
	}
	fmt.Fprintf(&b, "%s\n", m.Description)
	for _, t := range m.Traces {
		fmt.Fprintf(&b, "\n### %s. %s\n\n%s\n\n", t.ID, one(t.Title), t.Description)
		for _, l := range t.Locations {
			fmt.Fprintf(&b, "- **%s** %s %s: %s\n", l.ID, one(l.Title), cite(l), one(l.Description))
		}
		if t.Guide != "" {
			fmt.Fprintf(&b, "\n%s\n", t.Guide)
		}
	}
	return b.String()
}
