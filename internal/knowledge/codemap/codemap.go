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

type Trace struct {
	ID          string     `json:"id"` // 1, 2, ...
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Guide       string     `json:"guide"` // Markdown: how the locations work together
	Locations   []Location `json:"locations"`
}

type Location struct {
	ID          string `json:"id"` // 1a, 1b, ...
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

// ErrInvalid: the model's reply was no map, twice, or the second map had no
// location the sources show.
var ErrInvalid = errors.New("the model's reply was not a code map")

// maxEcho bounds how much of a reply that was no map goes back with the
// request to try again: enough to rework, not a whole runaway reply.
const maxEcho = 8000

// Draw asks model for the map of r's question and grounds it in r.Hits. A
// reply that is no JSON map, or whose locations the sources do not show, is
// asked for once more. drawing is told how much of the replies has come, as
// it comes. The map has no traces only when the model found nothing to map.
func Draw(ctx context.Context, model *ai.OpenAI, r Request, drawing func(chars int)) (Map, error) {
	msgs, n := prompt(r), 0
	for try := 0; ; try++ {
		reply, err := model.Stream(ctx, msgs, func(d string) {
			if n += len(d); drawing != nil {
				drawing(n)
			}
		})
		if err != nil {
			return Map{}, err
		}
		var m Map
		parsed := ai.ParseJSON(reply, &m) && m.Traces != nil
		if parsed && len(m.Traces) == 0 {
			return m, nil // nothing to map, as the rules let it say
		}
		if parsed {
			if ground(&m, r.Hits); len(m.Traces) > 0 {
				return m, nil
			}
		}
		if try == 1 {
			return Map{}, ErrInvalid
		}
		note := retryNote
		if parsed {
			note = groundNote
		}
		// Without a reply to show (it only thought), the note joins the request:
		// some servers refuse an empty assistant turn, or two user turns in a row.
		echo := strings.TrimSpace(ai.StripThinking(reply))
		if echo == "" {
			msgs[len(msgs)-1].Content += "\n\n" + note
			continue
		}
		if len(echo) > maxEcho {
			echo = strings.ToValidUTF8(echo[:maxEcho], "") + "…"
		}
		msgs = append(msgs, ai.Message{Role: "assistant", Content: echo}, ai.Message{Role: "user", Content: note})
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
