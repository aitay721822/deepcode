package trace

import (
	"encoding/json"
	"testing"
	"time"
)

// TestTake: a trace keeps each call with its arguments as JSON and when it
// ran, when the gathering ended, the sources shown, dropped, evicted and
// refused with their origins, the bytes, the context's window and the results
// masked, the turns summed up over every compact event, the citations resolved,
// and the words streamed, which a "done" event does not change.
func TestTake(t *testing.T) {
	var tr Trace
	type args struct {
		Query string `json:"query"`
	}
	tr.Take("search", map[string]any{"round": 1, "tool": "search_code", "args": args{"Mount"}}, 1234*time.Millisecond)
	tr.Take("search", map[string]any{"round": 2, "tool": "read_file", "args": map[string]any{"path": "mux.go"}}, 4567*time.Millisecond)
	tr.Take("compact", map[string]any{"summarized": 4}, time.Second)
	tr.Take("compact", map[string]any{"masked": 2}, 5*time.Second)
	tr.Take("compact", map[string]any{"summarized": 1}, 6*time.Second)
	tr.Take("evidence", map[string]any{"sources": []map[string]any{{"path": "mux.go", "startLine": 3, "endLine": 9, "round": 2, "tool": "read_file"},
		{"path": "tree.go", "startLine": 1, "endLine": 2, "round": 1, "tool": "first"}},
		"dropped": []map[string]any{{"path": "x.go", "startLine": 1, "endLine": 5, "round": 2, "tool": "search_code", "chars": 70}},
		"evicted": []map[string]any{{"path": "y.go", "startLine": 2, "endLine": 4, "round": 3, "tool": "users", "from": "first"}},
		"refused": []map[string]any{{"path": "z.go", "startLine": 1, "endLine": 200, "round": 3, "tool": "read_file"}},
		"chars":   map[string]int{"rendered": 30, "budget": 40}, "masked": 2,
		"window": map[string]int{"context": 524288, "threshold": 419430, "evidence": 40, "history": 9, "background": 0}}, 9*time.Second)
	tr.Take("delta", "It mounts [mux.go:3-4]", 10*time.Second)
	tr.Take("delta", " and [x.go:1-2].", 11*time.Second)
	tr.Take("cited", map[string]any{"resolved": 2}, 12*time.Second)
	tr.Take("done", map[string]any{"content": "It mounts."}, 13*time.Second)
	want := Trace{Calls: []Call{{1, "search_code", json.RawMessage(`{"query":"Mount"}`), 1.23}, {2, "read_file", json.RawMessage(`{"path":"mux.go"}`), 4.57}}, Gathered: 9,
		Shown: []Source{{"mux.go:3-9", 2, "read_file", "", 0}, {"tree.go:1-2", 1, "first", "", 0}}, Dropped: []Source{{"x.go:1-5", 2, "search_code", "", 70}},
		Evicted: []Source{{"y.go:2-4", 3, "users", "first", 0}}, Refused: []Source{{"z.go:1-200", 3, "read_file", "", 0}},
		Chars: map[string]int{"rendered": 30, "budget": 40}, Window: map[string]int{"context": 524288, "threshold": 419430, "evidence": 40, "history": 9, "background": 0},
		Masked: 2, Summarized: 5, Resolved: 2, Raw: "It mounts [mux.go:3-4] and [x.go:1-2]."}
	got, _ := json.Marshal(tr)
	if w, _ := json.Marshal(want); string(got) != string(w) {
		t.Errorf("%s, want %s", got, w)
	}
}
