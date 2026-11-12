package codemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
)

var hits = []retrieval.Hit{
	{Path: "src/server/router.go", StartLine: 10, EndLine: 14, Content: "func Router() {\n\tr := gin.New()\n\tr.Use(auth)\n\treturn r\n}"},
	{Path: "src/server/auth.go", StartLine: 1, EndLine: 3, Content: "func auth(c *gin.Context) {\r\n\tcheck(c.GetHeader(\"Authorization\"))\r\n}"},
	{Path: "a/util.go", StartLine: 1, EndLine: 1, Content: "func Helper() {}"},
	{Path: "b/util.go", StartLine: 1, EndLine: 1, Content: "func Helper() {}"},
}

// TestGround: a location keeps a line that reads as its code, or moves to the
// nearest one of its file that does; one without code, whose code or file the
// sources lack (or two files end as its path), or that repeats a line, is
// dropped, and with it a trace left empty; what is kept is numbered anew.
func TestGround(t *testing.T) {
	m := Map{Traces: []Trace{
		{Title: "made up", Locations: []Location{{Path: "src/server/router.go", Line: 11, Code: "db.Open()"}, {Path: "nope.go", Line: 1, Code: "x := 1"}}},
		{Title: "request", Locations: []Location{
			{Path: "src/server/router.go", Line: 11, Code: "r := gin.New()"},
			{Path: "server/router.go", Line: 40, Code: "r.Use(auth)"},                               // a dropped folder, a wrong line
			{Path: `.\src\server\auth.go`, Line: 2, Code: `2: check(c.GetHeader("Authorization"))`}, // Windows path, the number copied
			{Path: "src/server/auth.go", Line: 3},                                                   // no code
			{Path: "src/server/router.go", Line: 12, Code: "r := gin.New()"},                        // line 11 again
			{Path: "util.go", Line: 1, Code: "func Helper() {}"},                                    // a/util.go or b/util.go
			{Path: "src/server/router.go", Line: 1, Code: "}"},                                      // a brace names no line but itself
		}},
	}}
	ground(&m, hits)
	var got []string
	for _, tr := range m.Traces {
		for _, l := range tr.Locations {
			got = append(got, fmt.Sprintf("%s %s:%d %s", l.ID, l.Path, l.Line, l.Code))
		}
	}
	want := []string{"1a src/server/router.go:11 r := gin.New()", "1b src/server/router.go:12 r.Use(auth)",
		`1c src/server/auth.go:2 check(c.GetHeader("Authorization"))`, "1d src/server/router.go:14 }"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(m.Traces) != 1 || m.Traces[0].ID != "1" || m.Traces[0].Title != "request" {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestReads: code reads as a source line when the same but for spacing, when
// all of it is at least half the line, or when it holds the whole line, run
// into the next; a short fragment reads only as a line that is just that.
func TestReads(t *testing.T) {
	for _, c := range []struct {
		text, code string
		ok         bool
	}{
		{"\tr.Use(auth)", "r.Use(auth)", true},
		{"r.Use(auth) // x", "r.Use(auth)", true},                    // a trailing comment left out
		{"r.Use(auth) // check every request", "r.Use(auth)", false}, // less than half the line
		{"x = b(c)", "b(c)", false},                                  // a short fragment
		{"r := gin.New()", "r := gin.New() r.Use(auth)", true},       // two lines run together
		{"return r", "return r x := 1", false},                       // too short a line to be one of two
		{"}", "}", true}, {"}", "})", false}, {"", "", false},
	} {
		if reads(c.text, c.code) != c.ok {
			t.Errorf("reads(%q, %q) = %v", c.text, c.code, !c.ok)
		}
	}
}

// TestGroundBounds: a map keeps at most maxTraces traces of maxLocations.
func TestGroundBounds(t *testing.T) {
	var lines []string
	for i := range maxLocations + 2 {
		lines = append(lines, fmt.Sprintf("call%02d()", i))
	}
	big := []retrieval.Hit{{Path: "big.go", StartLine: 1, EndLine: len(lines), Content: strings.Join(lines, "\n")}}
	var m Map
	for range maxTraces + 2 {
		var locs []Location
		for i, l := range lines {
			locs = append(locs, Location{Path: "big.go", Line: i + 1, Code: l})
		}
		m.Traces = append(m.Traces, Trace{Locations: locs})
	}
	ground(&m, big)
	if len(m.Traces) != maxTraces || len(m.Traces[0].Locations) != maxLocations {
		t.Fatalf("%d traces, the first of %d locations", len(m.Traces), len(m.Traces[0].Locations))
	}
}

const good = `{"title":"Auth","description":"How requests are checked.","traces":[{"title":"Request","description":"d","guide":"g",` +
	`"locations":[{"title":"Router","description":"uses auth","path":"src/server/router.go","line":12,"code":"r.Use(auth)"}]}]}`

// model answers each call with the next of replies, keeping the messages
// each call sent.
func model(t *testing.T, replies ...string) (*ai.OpenAI, *[][]ai.Message) {
	var asked [][]ai.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []ai.Message }
		json.NewDecoder(r.Body).Decode(&body)
		asked = append(asked, body.Messages)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": replies[len(asked)-1]}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)
	return &ai.OpenAI{BaseURL: srv.URL, ModelName: "m"}, &asked
}

// TestDraw: a map of lines the sources lack is asked for once more, after the
// request, with the map (cut to maxEcho) and groundNote, and fails as
// ErrInvalid the second time; an answer that is no map fails at once with
// ai.ErrSchema; the count drawing hears runs on over both replies; the retry
// is no larger than Fixed holds, a map past maxEcho filling it; an empty
// trace list is the model's answer; the map is written out with a citation
// per location. So under either ai.Schema setting.
func TestDraw(t *testing.T) {
	made := strings.Replace(good, "src/server/router.go", "nope.go", 1)
	long := strings.Replace(made, `"title":"Auth"`, `"title":"x`+strings.Repeat("é", maxEcho)+`"`, 1) // 2 bytes each, after 11: cut inside one
	ascii := strings.Replace(made, `"title":"Auth"`, `"title":"x`+strings.Repeat("e", maxEcho)+`"`, 1)
	req := Request{Question: "How is auth done?", Hits: hits}
	for _, c := range []struct {
		replies []string
		traces  int
		err     error
		echo    string // the retry's map, before groundNote
	}{
		{[]string{"Sure! Here is the map:"}, 0, ai.ErrSchema, ""},
		{[]string{"{}"}, 0, ai.ErrSchema, ""}, // JSON, but no map: not taken for one with nothing in it
		{[]string{long, good}, 1, nil, long[:maxEcho-1] + "…"},
		{[]string{ascii, good}, 1, nil, ascii[:maxEcho] + "…"},
		{[]string{made, good}, 1, nil, made},
		{[]string{made, made}, 0, ErrInvalid, made},
		{[]string{`{"title":"","description":"","traces":[]}`}, 0, nil, ""},
	} {
		for _, schema := range []string{ai.SchemaServer, ai.SchemaPrompt} {
			m, asked := model(t, c.replies...)
			m.Schema = schema
			var heard []int
			got, err := Draw(context.Background(), m, req, func(n int) { heard = append(heard, n) })
			total := 0
			for _, r := range c.replies {
				total += len(r)
			}
			if !errors.Is(err, c.err) || len(got.Traces) != c.traces || len(*asked) != len(c.replies) || heard[len(heard)-1] != total {
				t.Errorf("%.30q: %+v %v after %d calls, heard %v", c.replies[0], got, err, len(*asked), heard)
				continue
			}
			if len(*asked) < 2 {
				continue
			}
			first, retry, spent := (*asked)[0], (*asked)[1], 0
			for _, msg := range retry {
				spent += utf8.RuneCountInString(msg.Content)
			}
			fixed := Fixed(req, m, func(p ai.Prompt) int { n, _ := p.Size(utf8.RuneCountInString); return n }) // in characters, at least as many as tokens
			if spent > fixed || c.echo == ascii[:maxEcho]+"…" && fixed-spent != len("…")-1 {
				t.Errorf("%.30q: the retry sent %d characters, Fixed %d", c.replies[0], spent, fixed)
			}
			if n := len(retry); n != len(first)+2 || retry[n-2] != (ai.Message{Role: "assistant", Content: c.echo}) ||
				retry[n-1] != (ai.Message{Role: "user", Content: groundNote}) || retry[n-3].Content != first[len(first)-1].Content {
				t.Errorf("%.30q: the retry ends %q", c.replies[0], retry[max(n-3, 0):])
			}
		}
	}
	m, _ := model(t, good)
	got, _ := Draw(context.Background(), m, Request{Hits: hits}, nil)
	md := got.Markdown(func(l Location) string { return fmt.Sprintf("[%s:%d](u)", l.Path, l.Line) })
	if !strings.Contains(md, "- **1a** Router [src/server/router.go:12](u): uses auth") || !strings.HasPrefix(md, "## Auth") {
		t.Errorf("markdown:\n%s", md)
	}
}

// TestFormat: the map the model is asked for is a Map without ids, which
// ground gives the traces and locations it keeps.
func TestFormat(t *testing.T) {
	b, _ := json.Marshal(format.JSON)
	if s := string(b); !strings.Contains(s, `"locations"`) || !strings.Contains(s, `"guide"`) || strings.Contains(s, `"id"`) {
		t.Fatal(s)
	}
}
