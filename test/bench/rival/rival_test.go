package rival

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCited(t *testing.T) {
	answer := "See [mux.go:10-20](https://github.com/go-chi/chi/blob/9f8adb1aabaa8a91180a821111d3f8e668d1c0bf/mux.go#L10-L20), " +
		"[a b.cs:1-2](https://github.com/o/r/blob/abc1234/src/a%20b.cs#L1-L2), again [mux.go:30-31](https://github.com/go-chi/chi/blob/9f8adb1/mux.go#L30-L31), " +
		"[x.go:1-2](https://gitlab.com/o/r/-/blob/0123456789abcdef/pkg/x.go#L1-2), [q.go](https://github.com/o/r/blob/abcdef0/q.go?plain=1), " +
		"[bad](https://github.com/o/r/blob/abcdef0/100%zz.go#L1-L2), the [guide](https://example.com/guide), a branch's " +
		"[file](https://github.com/o/r/blob/notasha/y.go), a six-digit one's [file](https://github.com/o/r/blob/abc123/z.go) and a whole file, [b.go](https://github.com/o/r/blob/abcdef0/b.go)."
	if got, want := Cited(answer), []string{"mux.go", "src/a b.cs", "pkg/x.go", "q.go", "100%zz.go", "b.go"}; !slices.Equal(got, want) {
		t.Fatalf("Cited = %q, want %q", got, want)
	}
	if got := Cited("No indexed source matched."); got != nil {
		t.Fatalf("an answer citing nothing cites %q", got)
	}
}

func TestMeasure(t *testing.T) {
	at := func(f string) bool { return f != "gone.go" }
	o := Measure([]string{"a.go", "b.go", "c.go", "gone.go"}, []string{"a.go", "c.go", "x.go"}, at)
	want := Overlap{DeepWiki: []string{"a.go", "b.go", "c.go"}, Gone: []string{"gone.go"}, Deepcode: []string{"a.go", "c.go", "x.go"},
		Shared: []string{"a.go", "c.go"}, Ratio: 2.0 / 3, Count: 1}
	if !reflect.DeepEqual(o, want) || !o.Even() {
		t.Fatalf("Measure = %+v (even %v), want %+v, even", o, o.Even(), want)
	}
	// chi's matched-route-pattern question in runs/20261113T151306Z: DeepWiki cites five files, deepcode two of them.
	chi := Measure([]string{"context.go", "context_test.go", "mux.go", "pattern_test.go", "tree.go"}, []string{"context.go", "tree.go"}, at)
	if chi.Ratio != 0.4 || chi.Count != 0.4 || chi.Even() {
		t.Fatalf("chi's question: ratio %v, count %v, even %v; want 0.4, 0.4, not even", chi.Ratio, chi.Count, chi.Even())
	}
	if half := Measure([]string{"a.go", "b.go"}, []string{"a.go"}, at); !half.Even() {
		t.Fatalf("half the files (%+v) is not even", half)
	}
	if many := Measure([]string{"a.go", "b.go", "c.go"}, []string{"a.go", "x.go", "y.go", "z.go"}, at); many.Even() || many.Count < 1 {
		t.Fatalf("one of three files among many others (%+v) is even", many)
	}
	for _, deepwiki := range [][]string{{"gone.go"}, nil} {
		if none := Measure(deepwiki, []string{"a.go"}, at); none.Measured() || none.Even() || none.Ratio != 0 {
			t.Fatalf("DeepWiki citing %q: %+v, measured %v, even %v; want neither", deepwiki, none, none.Measured(), none.Even())
		}
	}
}

func TestSection(t *testing.T) {
	at := func(f string) bool { return f != "gone.go" }
	even := Measure([]string{"a.go"}, []string{"a.go"}, at)
	uneven := Measure([]string{"a.go", "b.go", "c.go", "d.go", "e.go"}, []string{"a.go", "b.go"}, at)
	mid := Measure([]string{"a.go", "b.go", "gone.go"}, []string{"a.go", "x.go", "y.go"}, at)
	failed := Measure([]string{"a.go"}, nil, at)
	uncited := Measure(nil, []string{"a.go"}, at)
	md := Section([]Row{{Repo: "chi", Question: "q1", Commit: "a54874f0", Overlap: &even}, {Repo: "chi", Question: "q2", Overlap: &uneven},
		{Repo: "chi", Question: "q4", Overlap: &mid}, {Repo: "axum", Question: "q3"}, {Repo: "gson", Question: "q5", Failed: true, Overlap: &failed},
		{Repo: "zustand", Question: "q6", Overlap: &uncited}})
	for _, want := range []string{
		"2 of 6 questions even. Not even for want of a measure: 1 without a captured DeepWiki answer, 1 whose DeepWiki answer cites no file head holds. " +
			"1 of deepcode's answers failed. Median ratio 0.45, median count 0.70 over the 4 measured.",
		"| chi | q1 | a54874f0 | a.go | a.go | a.go | 1.00 | 1.00 | yes | – |",
		"| chi | q2 | – | a.go, b.go, c.go, d.go, e.go | a.go, b.go | a.go, b.go | 0.40 | 0.40 | no | – |",
		"| chi | q4 | – | a.go, b.go | a.go, x.go, y.go | a.go | 0.50 | 1.50 | yes | gone.go |",
		"| axum | q3 | – | not captured |",
		"| gson | q5 | – | a.go | failed | – | 0.00 | 0.00 | no | – |",
		"| zustand | q6 | – | – | a.go | – | – | – | unmeasured | – |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the section lacks %q:\n%s", want, md)
		}
	}
	if m := median([]float64{3, 1, 2}); m != 2 {
		t.Errorf("the median of 3, 1 and 2 is %v", m)
	}
}

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deepwiki", "answers.json")
	if c, err := Load(path); err != nil || len(c.Answers) != 0 {
		t.Fatalf("no capture yet loads as %+v, %v; want an empty one", c, err)
	}
	c := Capture{Answers: []Answer{
		{Repo: "o/a", Question: "How?", Captured: "2026-11-14T00:00:00Z", Text: "A.", Slug: "s1", Commit: "c1", Refs: []Ref{{"a.go", 1, 2}}, Read: []string{"a.go"}},
		{Repo: "o/b", Question: "How?", Text: "B."},
	}}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("Load after Save = %+v, %v; want %+v", got, err, c)
	}
	if a, ok := got.Find("o/b", "How?"); !ok || a.Text != "B." {
		t.Fatalf("Find(o/b) = %+v, %v; want B's answer, as each repository asks its own", a, ok)
	}
	if _, ok := got.Find("o/c", "How?"); ok {
		t.Fatal("found an answer for a repository asked nothing")
	}
}

// kept is a search as DeepWiki's API keeps it, trimmed: an older query, then the last one with its files
// read (one twice), a chunk of the answer and its references, lines from 0.
const kept = `{"title":"q","queries":[
	{"state":"done","repo_context_ids":["v1/PUBLIC/go-chi/chi/0ld0ld0l"],"response":[
	{"type":"reference","data":{"file_path":"Repo go-chi/chi: old.go","range_start":0,"range_end":1}}]},
	{"state":"done","repo_context_ids":["v1.9.9.5/PUBLIC/go-chi/chi/a54874f0"],"response":[
	{"type":"file_contents","data":["go-chi/chi","context.go","package chi"]},
	{"type":"file_contents","data":["go-chi/chi","context.go","package chi"]},
	{"type":"file_contents","data":["go-chi/chi"]},
	{"type":"chunk","data":"## Answer"},
	{"type":"reference","data":{"file_path":"Repo go-chi/chi: context.go","range_start":108,"range_end":122}},
	{"type":"reference","data":{"file_path":"Repo go-chi/chi: middleware/strip.go","range_start":0,"range_end":9}},
	{"type":"reference","data":{"file_path":"Repo go-chi/chi: context.go","range_start":122,"range_end":132}}]}]}`

func TestAsk(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/q/how-x_1":
			w.Write([]byte(kept))
		case "/q/busy_2":
			w.Write([]byte(`{"queries":[{"state":"running","response":[]}]}`))
		case "/q/odd_4":
			w.Write([]byte(`{"queries":[{"state":"done","response":[{"type":"reference","data":"context.go"}]}]}`))
		case "/q/cut_5":
			w.Write([]byte(`{"queries":[{"state":"do`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	link := func(slug string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Use RoutePattern.\n\n"}, &mcp.TextContent{Text: "View this search on DeepWiki: https://deepwiki.com/search/" + slug + "\n"}}}
	}
	replies := map[string]*mcp.CallToolResult{
		"ok": link("how-x_1"), "busy": link("busy_2"), "lost": link("lost_3"), "odd": link("odd_4"), "cut": link("cut_5"),
		"no link": {Content: []mcp.Content{&mcp.TextContent{Text: "An answer naming no search."}}},
		"refused": {IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Repository not found"}}},
	}
	a := Asker{Session: session(t, replies), HTTP: api.Client(), Searches: api.URL + "/q/"}
	got, err := a.Ask(t.Context(), "go-chi/chi", "ok")
	if err != nil {
		t.Fatal(err)
	}
	want := Answer{Repo: "go-chi/chi", Question: "ok", Captured: got.Captured, Text: "Use RoutePattern.\n\nView this search on DeepWiki: https://deepwiki.com/search/how-x_1\n",
		Slug: "how-x_1", Commit: "a54874f0", Refs: []Ref{{"context.go", 109, 123}, {"middleware/strip.go", 1, 10}, {"context.go", 123, 133}}, Read: []string{"context.go"}}
	if !reflect.DeepEqual(got, want) || got.Captured == "" {
		t.Fatalf("Ask = %+v\nwant %+v", got, want)
	}
	if files := got.Files(); !slices.Equal(files, []string{"context.go", "middleware/strip.go"}) {
		t.Fatalf("Files = %q", files)
	}
	for q, says := range map[string]string{"busy": "not done", "lost": "404", "odd": "a reference", "cut": "unexpected EOF", "no link": "names no search", "refused": "Repository not found"} {
		if _, err := a.Ask(t.Context(), "go-chi/chi", q); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("asking %q: %v, want an error saying %q", q, err, says)
		}
	}
	for path, want := range map[string]string{"context.go": "context.go", "Repo go-chi/chi": "Repo go-chi/chi", "Repo o/r: a: b.go": "a: b.go"} {
		if got := inRepo(path); got != want {
			t.Errorf("inRepo(%q) = %q, want %q", path, got, want)
		}
	}
}

// session is a client's session with an MCP server in memory whose ask_question replies to a question
// with replies[question].
func session(t *testing.T, replies map[string]*mcp.CallToolResult) *mcp.ClientSession {
	type args struct {
		RepoName string `json:"repoName"`
		Question string `json:"question"`
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "deepwiki", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "ask_question"}, func(_ context.Context, _ *mcp.CallToolRequest, in args) (*mcp.CallToolResult, any, error) {
		return replies[in.Question], nil, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "bench", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}
