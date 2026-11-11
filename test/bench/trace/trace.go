// Package trace keeps how a bench answer was gathered and written, and when,
// as the chat service's events tell it.
package trace

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Trace is an answer's: the calls the gathering ran, each when it ran; when
// the gathering ended; the sources the answer was shown and the hits gathered
// that it was not ("dropped", which the gathering's budget keeps empty), each
// with the round and tool that found it; the hits that budget evicted and
// refused, each with the round and tool of the call that did it; the sources'
// bytes as rendered, against the budget; the model's context, threshold and
// shares, the tool results masked and the turns summed up to fit it; the
// references resolved to a path that differs from the one written; and the
// answer as the model streamed it, before its citations were checked, so that
// a citation the check dropped can still be read. Times are seconds since the
// question.
type Trace struct {
	Calls      []Call         `json:"calls"`
	Gathered   float64        `json:"gathered"` // the evidence event's time
	Shown      []Source       `json:"shown"`
	Dropped    []Source       `json:"dropped"`
	Evicted    []Source       `json:"evicted"`
	Refused    []Source       `json:"refused"`
	Chars      map[string]int `json:"chars"`  // rendered, budget
	Window     map[string]int `json:"window"` // context, threshold, evidence, history, background
	Masked     int            `json:"masked,omitempty"`
	Summarized int            `json:"summarized,omitempty"`
	Resolved   int            `json:"resolved"`
	Raw        string         `json:"raw"`
}

type Call struct {
	Round int             `json:"round"`
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args"`
	At    float64         `json:"at"` // when it ran: its reply had come
}

type Source struct {
	At    string `json:"at"` // path:start-end
	Round int    `json:"round"`
	Tool  string `json:"tool"`
	From  string `json:"from,omitempty"`  // the tool that had found an evicted hit
	Chars int    `json:"chars,omitempty"` // a dropped hit's bytes as gathered
}

// Take records one of an answer's events, which came at since the question.
// An answer has one evidence event, the gathering's: the words streamed are
// its draft's.
func (tr *Trace) Take(event string, data any, at time.Duration) {
	seconds := math.Round(at.Seconds()*100) / 100
	switch d := data.(type) {
	case map[string]any:
		switch event {
		case "search":
			round, _ := d["round"].(int)
			tool, _ := d["tool"].(string)
			args, _ := json.Marshal(d["args"])
			tr.Calls = append(tr.Calls, Call{Round: round, Tool: tool, Args: args, At: seconds})
		case "evidence":
			tr.Gathered = seconds
			tr.Shown, tr.Dropped, tr.Evicted, tr.Refused = sources(d["sources"]), sources(d["dropped"]), sources(d["evicted"]), sources(d["refused"])
			tr.Chars, _ = d["chars"].(map[string]int)
			tr.Window, _ = d["window"].(map[string]int)
			tr.Masked, _ = d["masked"].(int)
		case "compact":
			n, _ := d["summarized"].(int)
			tr.Summarized += n
		case "cited":
			tr.Resolved, _ = d["resolved"].(int)
		}
	case string:
		if event == "delta" {
			tr.Raw += d
		}
	}
}

// Clock times an answer by its events. An evidence event starts a draft at
// the words that follow it. One that no words follow, after a rewrite, lists
// the standing draft's sources again: the rewrite failed, and the draft
// before it is the answer.
type Clock struct {
	starts  []time.Duration // each draft's first words
	pending bool            // an evidence event that no words followed yet
}

// Tick records event, which came at now.
func (c *Clock) Tick(event string, now time.Duration) {
	switch {
	case event == "evidence":
		c.pending = true
	case event == "delta" && c.pending:
		c.pending, c.starts = false, append(c.starts, now)
	}
}

// Read is when the answer's first words came, when those of the draft it
// ended with, and how many drafts were written after the first.
func (c *Clock) Read() (first, begun time.Duration, rewrites int) {
	n := len(c.starts)
	if n == 0 {
		return 0, 0, 0
	}
	last := n - 1
	if c.pending && n > 1 {
		last--
	}
	return c.starts[0], c.starts[last], n - 1
}

func sources(v any) []Source {
	list, _ := v.([]map[string]any)
	out := make([]Source, len(list))
	for i, s := range list {
		out[i].At = fmt.Sprintf("%v:%v-%v", s["path"], s["startLine"], s["endLine"])
		out[i].Round, _ = s["round"].(int)
		out[i].Tool, _ = s["tool"].(string)
		out[i].From, _ = s["from"].(string)
		out[i].Chars, _ = s["chars"].(int)
	}
	return out
}
