package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"deepcode/internal/platform/ai"
)

// answer is what a fake Ask returns for one request.
type answer struct {
	v   int
	err error
}

// TestRun: Run asks once, checks, repairs once after the reply with the note
// (or jsonNote for a reply that does not read), checks again and has code fix
// the rest, counting each rule at each step; Minor violations alone ask no
// repair; a loose reply Check finds nothing in, first or repair, is
// schema.other, kept with the validator's words; the note for an unread reply
// quotes only the reader's words; a repair that fails, of a readable reply, leaves the
// first standing, unless the context ended; of an unreadable one, fails.
func TestRun(t *testing.T) {
	loose := fmt.Errorf("%w: has x", ErrLoose)
	unread := fmt.Errorf("m: the answer is %w: %w", &ai.DecodeError{Name: "k", Err: errors.New("bad")}, ai.ErrSchema)
	for _, c := range []struct {
		name    string
		answers []answer
		merge   bool
		want    int
		err     error
		counts  map[Rule]Count
		notes   []string // the repair's message, when one is asked
		other   string
	}{
		{"clean", []answer{{5, nil}}, false, 5, nil, map[Rule]Count{}, nil, ""},
		{"repaired", []answer{{-1, nil}, {7, nil}}, false, 7, nil, map[Rule]Count{"neg": {1, 0, 0}}, []string{"fix: neg at -1"}, ""},
		{"still broken", []answer{{-1, nil}, {-2, nil}}, false, 0, nil, map[Rule]Count{"neg": {1, 1, 1}}, []string{"fix: neg at -1"}, ""},
		{"merged", []answer{{-1, nil}, {7, nil}}, true, 6, nil, map[Rule]Count{"neg": {1, 0, 0}}, []string{"fix: neg at -1"}, ""},
		{"minor only", []answer{{150, nil}}, false, 100, nil, map[Rule]Count{"big": {1, 0, 1}}, nil, ""},
		{"minor and not", []answer{{-1, nil}, {150, nil}}, false, 100, nil, map[Rule]Count{"neg": {1, 0, 0}, "big": {0, 1, 1}}, []string{"fix: neg at -1"}, ""},
		{"unread, then read", []answer{{0, unread}, {9, nil}}, false, 9, nil, map[Rule]Count{"k.json": {1, 0, 0}}, []string{"Your reply is not the JSON of k (bad). Reply again with only the JSON asked for."}, ""},
		{"unread twice", []answer{{0, unread}, {0, unread}}, false, 0, ai.ErrSchema, map[Rule]Count{"k.json": {1, 1, 0}}, nil, ""},
		{"read, repair unread", []answer{{-1, nil}, {0, unread}}, false, 0, nil, map[Rule]Count{"neg": {1, 1, 1}}, []string{"fix: neg at -1"}, ""},
		{"loose, named", []answer{{-1, loose}, {3, nil}}, false, 3, nil, map[Rule]Count{"neg": {1, 0, 0}}, []string{"fix: neg at -1"}, ""},
		{"loose, unnamed", []answer{{4, loose}}, false, 4, nil, map[Rule]Count{"k.schema.other": {1, 0, 0}}, nil, loose.Error()},
		{"loose repair, unnamed", []answer{{-1, nil}, {3, loose}}, false, 3, nil, map[Rule]Count{"neg": {1, 0, 0}, "k.schema.other": {1, 0, 0}}, []string{"fix: neg at -1"}, loose.Error()},
		{"api error", []answer{{0, errors.New("503")}}, false, 0, errors.New("503"), map[Rule]Count{}, nil, ""},
		{"api error on repair", []answer{{-1, nil}, {0, errors.New("503")}}, false, 0, nil, map[Rule]Count{"neg": {1, 1, 1}}, []string{"fix: neg at -1"}, ""},
		{"unread, api error on repair", []answer{{0, unread}, {0, errors.New("503")}}, false, 0, errors.New("503"), map[Rule]Count{"k.json": {1, 0, 0}}, nil, ""},
		{"cancelled on repair", []answer{{-1, nil}, {0, context.Canceled}}, false, -1, context.Canceled, map[Rule]Count{"neg": {1, 0, 0}}, []string{"fix: neg at -1"}, ""},
	} {
		var asked []ai.Prompt
		ctx, cancel := context.WithCancel(context.Background())
		turn := Turn[int]{
			Kind: "k",
			Ask: func(_ context.Context, p ai.Prompt) (int, string, error) {
				a := c.answers[len(asked)]
				if asked = append(asked, p); errors.Is(a.err, context.Canceled) {
					cancel() // the job stopped while the repair was asked
				}
				return a.v, fmt.Sprint("reply ", len(asked)), a.err
			},
			Check: func(v int) []Violation {
				var vs []Violation
				if v < 0 {
					vs = append(vs, Violation{Rule: "neg", Where: fmt.Sprint(v)})
				}
				if v > 100 {
					vs = append(vs, Violation{Rule: "big", Minor: true})
				}
				return vs
			},
			Note: func(vs []Violation) string { return fmt.Sprintf("fix: %s at %s", vs[0].Rule, vs[0].Where) },
			Fix:  func(v *int) { *v = max(0, min(*v, 100)) },
		}
		if c.merge {
			turn.Merge = func(first, fix int, _ []Violation) int { return first + fix }
		}
		var counts Counts
		p := ai.Prompt{Static: "s", Dynamic: "d"}
		v, err := Run(ctx, p, turn, &counts)
		cancel()
		if v != c.want || (err == nil) != (c.err == nil) || c.err != nil && !errors.Is(err, c.err) && err.Error() != c.err.Error() {
			t.Errorf("%s: %d %v, want %d %v", c.name, v, err, c.want, c.err)
		}
		if got := counts.Snapshot(); !reflect.DeepEqual(got, c.counts) {
			t.Errorf("%s: counts %v, want %v", c.name, got, c.counts)
		}
		if counts.Other() != c.other {
			t.Errorf("%s: other %q, want %q", c.name, counts.Other(), c.other)
		}
		if len(asked) != len(c.answers) {
			t.Errorf("%s: %d requests, want %d", c.name, len(asked), len(c.answers))
		}
		if !reflect.DeepEqual(asked[0], p) {
			t.Errorf("%s: first request %+v", c.name, asked[0])
		}
		for i, note := range c.notes {
			if want := p.After("reply 1", note); !reflect.DeepEqual(asked[i+1], want) {
				t.Errorf("%s: repair %+v, want %+v", c.name, asked[i+1], want)
			}
		}
	}
}

// TestJSON: a reply that reads into T but fails the schema (an extra key) is
// returned read, with ErrLoose; one that is no JSON, or the wrong type, wraps
// ai.ErrSchema.
func TestJSON(t *testing.T) {
	type reply struct {
		A int `json:"a"`
	}
	f := ai.Schema[reply]("r")
	for _, c := range []struct {
		text  string
		want  int
		loose bool
		bad   bool
	}{
		{`{"a":1}`, 1, false, false},
		{`{"a":2,"x":3}`, 2, true, false},
		{`{}`, 0, true, false},
		{`oops`, 0, false, true},
		{`{"a":"s"}`, 0, false, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": c.text}, "finish_reason": "stop"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		}))
		v, text, err := JSON(&ai.OpenAI{BaseURL: srv.URL, ModelName: "m"}, f)(context.Background(), ai.Prompt{Dynamic: "q"})
		srv.Close()
		if v.A != c.want || text != c.text || errors.Is(err, ErrLoose) != c.loose || errors.Is(err, ai.ErrSchema) != c.bad || err == nil && (c.loose || c.bad) {
			t.Errorf("%s: %+v %q %v", c.text, v, text, err)
		}
	}
}

// TestDynamic: a schema built at request time checks an answer as Schema's
// do: its enum, integer range, required keys and no other key.
func TestDynamic(t *testing.T) {
	f, err := ai.Dynamic[map[string]any]("pick", map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"path", "n"},
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "enum": []string{"a.go", "b.go"}},
			"n":    map[string]any{"type": "integer", "minimum": 0, "maximum": 4},
		},
	})
	if err != nil || f.Name != "pick" {
		t.Fatalf("%+v %v", f, err)
	}
	if v, err := ai.Decode(f, `{"path":"b.go","n":4}`); err != nil || v["path"] != "b.go" {
		t.Fatalf("%v %v", v, err)
	}
	for _, bad := range []string{`{"path":"c.go","n":1}`, `{"path":"a.go","n":5}`, `{"path":"a.go"}`, `{"path":"a.go","n":1,"x":1}`, `a.go`} {
		if _, err := ai.Decode(f, bad); err == nil {
			t.Errorf("%s passed", bad)
		}
	}
}

// TestDynamicLimits: a schema past one of OpenAI's limits is refused before
// anything is sent, naming it; one at the limit is not.
func TestDynamicLimits(t *testing.T) {
	values := func(n, size int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%0*d", size, i)
		}
		return out
	}
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	enum := func(v []string) map[string]any { return map[string]any{"type": "string", "enum": v} }
	nest := func(depth int) map[string]any {
		s := obj(map[string]any{"x": map[string]any{"type": "string"}})
		for range depth - 1 {
			s = obj(map[string]any{"x": s})
		}
		return s
	}
	many := func(n int) map[string]any {
		p := map[string]any{}
		for i := range n {
			p[fmt.Sprintf("p%04d", i)] = map[string]any{"type": "string"}
		}
		return obj(p)
	}
	cases := []struct {
		name   string
		schema map[string]any
		limit  string // "": within every limit
	}{
		{"1000 enum values", obj(map[string]any{"a": enum(values(500, 3)), "b": enum(values(500, 3))}), ""},
		{"1001 enum values", obj(map[string]any{"a": enum(values(501, 3)), "b": enum(values(500, 3))}), "1001 enum values"},
		{"enum values in items and anyOf", obj(map[string]any{
			"a": map[string]any{"type": "array", "items": enum(values(600, 3))},
			"b": map[string]any{"anyOf": []any{enum(values(401, 3))}},
		}), "1001 enum values"},
		{"250 long values", obj(map[string]any{"a": enum(values(250, 61))}), ""},
		{"251 values past 15,000 characters", obj(map[string]any{"a": enum(values(251, 60))}), "15060 characters"},
		{"251 values within 15,000 characters", obj(map[string]any{"a": enum(values(251, 59))}), ""},
		{"depth 10", nest(10), ""},
		{"depth 11", nest(11), "11 levels"},
		{"5000 properties", many(5000), ""},
		{"5001 properties", many(5001), "5001 object properties"},
		{"120,000 characters", obj(map[string]any{"a": enum(values(200, 599))}), ""},
		{"120,001 characters", obj(map[string]any{"a": enum(values(200, 600))}), "120001 characters"},
		{"a const's characters", obj(map[string]any{"a": map[string]any{"const": strings.Repeat("x", 120000)}}), "120001 characters"},
		{"120,000 characters exactly", obj(map[string]any{"a": map[string]any{"const": strings.Repeat("x", 119999)}}), ""},
	}
	for _, c := range cases {
		_, err := ai.Dynamic[any]("s", c.schema)
		switch {
		case c.limit == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.limit != "" && (err == nil || !strings.Contains(err.Error(), c.limit)):
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.limit)
		}
	}
}
