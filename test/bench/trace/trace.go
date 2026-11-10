// Package trace keeps how a bench answer was gathered and written, as the chat
// service's events tell it.
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
// bytes as rendered, against the budget; the references resolved to a path
// that differs from the one written; and the answer as the model streamed it,
// before its citations were checked, so that a citation the check dropped can
// still be read. Times are seconds since the question.
type Trace struct {
	Calls    []Call         `json:"calls"`
	Gathered float64        `json:"gathered"` // the evidence event's time
	Shown    []Source       `json:"shown"`
	Dropped  []Source       `json:"dropped"`
	Evicted  []Source       `json:"evicted"`
	Refused  []Source       `json:"refused"`
	Chars    map[string]int `json:"chars"` // rendered, budget
	Resolved int            `json:"resolved"`
	Raw      string         `json:"raw"`
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
		case "cited":
			tr.Resolved, _ = d["resolved"].(int)
		}
	case string:
		if event == "delta" {
			tr.Raw += d
		}
	}
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
