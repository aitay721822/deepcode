package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/knowledge/retrieval/twins"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/jobs"
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	catalogstore "deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graph/store"
)

// file is one of a snapshot's, with its chunks' first and last lines and symbol.
type file struct {
	path, content string
	chunks        [][3]any
}

// files are the snapshot's; src/mux.go imports src/handler.go and names Handler.
var files = []file{
	{"go.mod", "module example", [][3]any{{1, 1, ""}}},
	{"src/tree.go", "package src\n" + strings.Repeat("// routing\n", 9) + "func findRoute(path string) *node {\n" + strings.Repeat("\treturn nil\n", 19),
		[][3]any{{1, 10, ""}, {11, 30, "findRoute"}}},
	{"src/handler.go", "package src\n\ntype Handler interface{ ServeHTTP() }", [][3]any{{1, 3, "Handler"}}},
	{"src/mux.go", "package src\n\nfunc (m *Mux) Handle(h Handler) { m.h = h }", [][3]any{{1, 3, "Handle"}}},
	{"gen/big.go", strings.Repeat("//\n", 249) + "//", [][3]any{{1, 250, ""}}},
}

// fixture is a Gatherer on a snapshot of files, asking model, and the request
// of question whose first search found go.mod's and handler.go's chunks.
func fixture(t *testing.T, model string) (Gatherer, Request) {
	g, req := seed(t, model, files, `INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'src/handler.go', 'Handler', 'src.Handler', 'interface', 3, 3)`,
		`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'src/mux.go', 'src/handler.go')`)
	first, err := g.Retriever.Store.Chunks(context.Background(), []int64{1, 4})
	if err != nil || len(first) != 2 {
		t.Fatal(first, err)
	}
	req.First = retrieval.Result{Hits: first, FTS: 2}
	return g, req
}

// seed is a Gatherer on snapshot 1 of fs, its statements run first, asking
// model, and a request with no first search.
func seed(t *testing.T, model string, fs []file, stmts ...string) (Gatherer, Request) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	exec := func(q string, args ...any) {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`)
	exec(`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`)
	for _, q := range stmts {
		exec(q)
	}
	for _, f := range fs {
		lines := strings.Split(f.content, "\n")
		exec(`INSERT INTO file_contents(hash, content) VALUES (?1, ?2)`, f.path, f.content)
		exec(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, ?1, 'go', ?1, ?2, ?3)`, f.path, len(f.content), len(lines))
		for _, c := range f.chunks {
			start, end := c[0].(int), c[1].(int)
			exec(`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES (1, ?, ?, ?, ?, ?, ?)`,
				f.path, c[2], start, end, strings.Join(lines[start-1:end], "\n"), fmt.Sprint(f.path, start))
		}
	}
	r := &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}}}
	g := Gatherer{Model: &ai.OpenAI{BaseURL: model, ModelName: "m"}, Retriever: r,
		Catalog: &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: &jobs.Queue{Store: &jobstore.Store{DB: db}}}}
	return g, Request{Snapshot: catalog.Ref{ID: 1, CommitSHA: "abc"}, Repo: "o/r", Question: "How is a route found?", Limits: Limits{Rounds: 4, Chars: 40000}}
}

// call is one tool call of a scripted reply.
type call struct{ name, args string }

// model is a Chat Completions server replying replies in turn, each the
// reply's text and calls, then text alone; it keeps the messages of every
// request.
func model(t *testing.T, replies ...[]call) (*httptest.Server, func() [][]map[string]any) {
	var mu sync.Mutex
	var asked [][]map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []map[string]any }
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		n := len(asked)
		asked = append(asked, body.Messages)
		mu.Unlock()
		msg := map[string]any{"role": "assistant", "content": "MARKER-PLAN: I will look around."}
		if n < len(replies) {
			var tcs []any
			for i, c := range replies[n] {
				tcs = append(tcs, map[string]any{"id": fmt.Sprintf("r%dc%d", n, i), "type": "function", "function": map[string]string{"name": c.name, "arguments": c.args}})
			}
			msg["tool_calls"] = tcs
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": msg}}})
	}))
	t.Cleanup(srv.Close)
	return srv, func() [][]map[string]any { mu.Lock(); defer mu.Unlock(); return slices.Clone(asked) }
}

// results are the tool results the model was sent, by call id.
func results(asked [][]map[string]any) map[string]string {
	out := map[string]string{}
	for _, msgs := range asked {
		for _, m := range msgs {
			if m["role"] == "tool" {
				out[m["tool_call_id"].(string)] = m["content"].(string)
			}
		}
	}
	return out
}

func paths(hits []retrieval.Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%s:%d-%d", h.Path, h.StartLine, h.EndLine))
	}
	return out
}

// TestGather: the model's calls run, each announced, a repeated one not run
// again; the evidence is the range read, then what users found, then what
// search_code found, then the first search's hits, a chunk the range read
// holds or one listed before left out. A search under a folder keeps the code
// under it. Nothing the model wrote is in the result or an event.
func TestGather(t *testing.T) {
	search := call{"search_code", `{"query":"ServeHTTP","path_prefix":""}`}
	srv, asked := model(t,
		[]call{search, {"read_file", `{"path":"tree.go","start_line":0,"end_line":0}`}, {"search_code", `{"query":"Mux","path_prefix":""}`}, {"search_code", `{"query":"module","path_prefix":"src/"}`}},
		[]call{search, {"users", `{"name":"Handler"}`}, {"done", ""}})
	g, req := fixture(t, srv.URL)
	var events []string
	res := g.Gather(context.Background(), req, func(event string, data any) {
		b, _ := json.Marshal(data)
		events = append(events, event+" "+string(b))
	})
	if want := []string{"src/tree.go:1-30", "src/mux.go:1-3", "src/handler.go:1-3", "go.mod:1-1"}; !slices.Equal(paths(res.Hits), want) {
		t.Errorf("evidence %q, want %q", paths(res.Hits), want)
	}
	if want := map[string]int{"search_code": 4, "read_file": 1, "users": 1, "done": 1}; res.Stop != StopDone || res.Rounds != 2 || !maps.Equal(res.Calls, want) {
		t.Errorf("stopped %q after %d rounds, calls %v", res.Stop, res.Rounds, res.Calls)
	}
	if want := []string{`search {"args":{"query":"ServeHTTP","path_prefix":""},"round":2,"tool":"search_code"}`,
		`search {"args":{"path":"tree.go","start_line":0,"end_line":0},"round":2,"tool":"read_file"}`,
		`search {"args":{"query":"Mux","path_prefix":""},"round":2,"tool":"search_code"}`,
		`search {"args":{"query":"module","path_prefix":"src/"},"round":2,"tool":"search_code"}`,
		`search {"args":{"name":"Handler"},"round":3,"tool":"users"}`}; !slices.Equal(events, want) {
		t.Errorf("events %q", events)
	}
	got := results(asked())
	if !strings.HasPrefix(got["r0c1"], "src/tree.go, lines 1-30 of 30:\n1: package src\n") || !strings.HasPrefix(got["r0c2"], "1. src/mux.go:1-3 Handle\n") ||
		!strings.HasPrefix(got["r0c0"], "1. src/handler.go:1-3 Handler\n") || got["r0c3"] != "No code matched." {
		t.Errorf("results %q", got)
	}
	if first := asked()[0]; !strings.Contains(first[1]["content"].(string), "The first search found:\n1. go.mod:1-1\n   1: module example\n2. src/handler.go:1-3 Handler") {
		t.Errorf("first request %v", first)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b)+strings.Join(events, ""), "MARKER") {
		t.Errorf("the model's own words left the loop: %s %q", b, events)
	}

	// Each hit's origin is the call that first found it: handler.go, which
	// search_code found again, the first search's; mux.go, listed with what
	// users found (it comes before handler.go), search_code's in the round
	// before. All of it fit: nothing was evicted or refused.
	tree, mux, handler := strings.TrimSuffix(files[1].content, "\n"), files[3].content, files[2].content
	if want := []Origin{{2, "read_file", len(tree)}, {2, "search_code", len(mux)}, {1, "first", len(handler)}, {1, "first", len("module example")}}; !slices.Equal(res.Origins, want) ||
		len(res.Evicted)+len(res.Refused) != 0 {
		t.Errorf("origins %v, evicted %v, refused %v", res.Origins, res.Evicted, res.Refused)
	}
	// The hits are what the tools returned, nothing of their origins in them,
	// so Format renders the same sources and takes the same hits as without.
	ctx := context.Background()
	users, err := g.Retriever.Users(ctx, 1, []string{"Handler"}, userHits)
	found, err2 := g.Retriever.Search(ctx, 1, "ServeHTTP", searchHits)
	if err != nil || err2 != nil || len(users) != 1 || len(found.Hits) == 0 || found.Hits[0].Path != "src/handler.go" {
		t.Fatal(users, found, err, err2)
	}
	want := []retrieval.Hit{{Path: "src/tree.go", StartLine: 1, EndLine: 30, Content: tree}, users[0], found.Hits[0], req.First.Hits[0]}
	sources, used := evidence.Format(res.Hits, req.Chars)
	wantSources, wantUsed := evidence.Format(want, req.Chars)
	if !reflect.DeepEqual(res.Hits, want) || sources != wantSources || !reflect.DeepEqual(used, wantUsed) || len(used) != len(res.Hits) || len(sources) != size(res.Hits) {
		t.Errorf("hits %+v\nwant %+v", res.Hits, want)
	}
}

// TestStops: each rule ends a gathering, and none fails it: the model's
// failure leaves the first search's hits.
func TestStops(t *testing.T) {
	read := func(p string) call { return call{"read_file", `{"path":"` + p + `","start_line":0,"end_line":0}`} }
	module := call{"search_code", `{"query":"module","path_prefix":""}`} // finds only what the first search found
	for name, tc := range map[string]struct {
		replies [][]call
		limits  Limits
		rounds  int
		hits    int
	}{
		StopAnswered: {nil, Limits{Rounds: 4, Chars: 40000}, 1, 2},
		StopRounds:   {[][]call{{read("go.mod")}, {read("src/mux.go")}, {read("src/tree.go")}}, Limits{Rounds: 2, Chars: 40000}, 2, 3},
		// The first search's hits fit, with 20 bytes left (set below); each read is refused, the first told.
		StopBudget: {[][]call{{read("src/tree.go")}, {read("gen/big.go")}}, Limits{Rounds: 4}, 2, 2},
		// Only what the first search found, then the same call again: two rounds in a row that added nothing.
		StopStalled: {[][]call{{module}, {module}}, Limits{Rounds: 4, Chars: 40000}, 2, 2},
		StopError:   {nil, Limits{Rounds: 4, Chars: 40000}, 0, 2},
		StopTime:    {nil, Limits{Rounds: 4, Chars: 40000, Time: 100 * time.Millisecond}, 0, 2},
	} {
		srv, asked := model(t, tc.replies...)
		switch name {
		case StopError:
			srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusBadRequest) })
		case StopTime:
			srv.Config.Handler = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body) // a handler that reads no body hears no disconnect
				<-r.Context().Done()
			})
		}
		g, req := fixture(t, srv.URL)
		if req.Limits = tc.limits; name == StopBudget {
			req.Chars = size(req.First.Hits) + 20
		}
		start := time.Now()
		res := g.Gather(context.Background(), req, func(string, any) {})
		if res.Stop != name || res.Rounds != tc.rounds || len(res.Hits) != tc.hits || time.Since(start) > 5*time.Second {
			t.Errorf("%s: stopped %q after %d rounds in %v, evidence %q", name, res.Stop, res.Rounds, time.Since(start), paths(res.Hits))
		}
		// The refusal is told in the read's result, after its lines.
		if got := results(asked())["r0c0"]; name == StopBudget && (!strings.HasPrefix(got, "src/tree.go, lines 1-30 of 30:\n") || !strings.HasSuffix(got, "20 bytes left of "+fmt.Sprint(req.Chars)+". Read a narrower range, or call done.\n")) {
			t.Errorf("the refused read was told %q", got)
		}
	}
}

// TestInvalidCalls: arguments not of a tool's schema, and a tool that does
// not exist, are told to the model as that call's result; they count as calls,
// a tool that does not exist as "unknown", announce nothing, add nothing, and
// never fail the question. Of a reply's calls past maxCalls, none runs, and
// each is told so.
func TestInvalidCalls(t *testing.T) {
	bad := []call{{"search_code", `{"query":5,"path_prefix":""}`}, {"read_file", `{"path":"src/mux.go"}`}, {"shell", `{"cmd":"ls"}`}}
	srv, asked := model(t, bad, bad)
	g, req := fixture(t, srv.URL)
	events := 0
	res := g.Gather(context.Background(), req, func(string, any) { events++ })
	if want := map[string]int{"search_code": 2, "read_file": 2, "unknown": 2}; res.Stop != StopStalled || events != 0 || len(res.Hits) != 2 || !maps.Equal(res.Calls, want) {
		t.Errorf("stopped %q, %d events, evidence %q, calls %v", res.Stop, events, paths(res.Hits), res.Calls)
	}
	got := results(asked())
	for id, want := range map[string]string{
		"r0c0": "Error: the arguments are not the JSON of search_code (", "r0c1": "Error: the arguments are not the JSON of read_file (",
		"r0c2": `Error: there is no tool "shell".`,
	} {
		if !strings.HasPrefix(got[id], want) {
			t.Errorf("%s is told %q", id, got[id])
		}
	}

	read := func(p string) call { return call{"read_file", `{"path":"` + p + `","start_line":1,"end_line":1}`} }
	srv, asked = model(t, []call{read("go.mod"), read("src/mux.go"), read("src/tree.go"), read("go.mod"), read("src/handler.go")})
	g, req = fixture(t, srv.URL)
	res = g.Gather(context.Background(), req, func(string, any) {})
	if got := results(asked()); res.Calls["read_file"] != maxCalls || got["r0c4"] != "Not run: at most 4 calls of a reply are run." ||
		got["r0c3"] != "Already called with these arguments: its result is above." || got["r0c2"] != "src/tree.go, lines 1-1 of 30:\n1: package src\n" {
		t.Errorf("five calls: %v, told %q", res.Calls, got)
	}
}

// TestReadIndexedOnly: read_file reads the snapshot's indexed files alone, by
// their path or the one path ending with it: no path reaches the disk, a path
// with .. segments is no path of the index, though it names a file, and a
// folder or a glob holding one file names none. Lines read before are not
// read again.
func TestReadIndexedOnly(t *testing.T) {
	g, req := fixture(t, "http://127.0.0.1:1")
	s := &state{g: g, req: req, res: Result{Calls: map[string]int{}}, held: map[int64]found{}, seen: map[string]bool{}}
	for _, p := range []string{"../../etc/passwd", "/etc/passwd", `C:\Windows\win.ini`, "src/../go.mod", "./go.mod", "src", "gen/", "src/h*.go"} {
		text, hits, err := s.read(context.Background(), readArgs{Path: p})
		if err != nil || len(hits) != 0 || !strings.HasPrefix(text, "Not in this snapshot: no one indexed file is at ") {
			t.Errorf("%s: %q %v %v", p, text, hits, err)
		}
	}
	for p, want := range map[string]string{"handler.go": "src/handler.go, lines 2-3 of 3:\n2: \n3: type Handler", "src/handler.go": "src/handler.go, lines 2-3 of 3:"} {
		text, hits, err := s.read(context.Background(), readArgs{Path: p, StartLine: 2, EndLine: 9})
		if err != nil || len(hits) != 1 || hits[0].StartLine != 2 || hits[0].EndLine != 3 || !strings.HasPrefix(text, want) {
			t.Errorf("%s: %q %v %v", p, text, hits, err)
		}
	}
	// At most maxRead lines a call, from the first when no line is given; none past the end.
	for _, tc := range []struct{ start, end, first, last int }{{0, 0, 1, 200}, {10, 900, 10, 209}, {240, 0, 240, 250}, {2, 1, 2, 201}} {
		text, hits, err := s.read(context.Background(), readArgs{Path: "big.go", StartLine: tc.start, EndLine: tc.end})
		if err != nil || len(hits) != 1 || hits[0].StartLine != tc.first || hits[0].EndLine != tc.last || !strings.HasPrefix(text, fmt.Sprintf("gen/big.go, lines %d-%d of 250:", tc.first, tc.last)) {
			t.Errorf("%d-%d: %.40q %v", tc.start, tc.end, text, err)
		}
	}
	for _, start := range []int{2, 5} {
		if text, hits, _ := s.read(context.Background(), readArgs{Path: "go.mod", StartLine: start}); text != "go.mod has 1 lines." || len(hits) != 0 {
			t.Errorf("past the end, from %d: %q", start, text)
		}
	}
	s.reads = []retrieval.Hit{{Path: "gen/big.go", StartLine: 50, EndLine: 99}, {Path: "gen/big.go", StartLine: 1, EndLine: 49}, {Path: "gen/big.go", StartLine: 200, EndLine: 250}}
	for _, tc := range []struct {
		path        string
		start, end  int
		first, last int
		text        string
	}{
		{"big.go", 10, 120, 100, 120, "gen/big.go, lines 100-120 of 250, the rest of lines 10-120 read above:\n100: //\n"},
		{"big.go", 150, 220, 150, 199, "gen/big.go, lines 150-199 of 250, the rest of lines 150-220 read above:\n"},
		{"big.go", 60, 70, 0, 0, "gen/big.go, lines 60-70: read above."},
		{"tree.go", 1, 5, 1, 5, "src/tree.go, lines 1-5 of 30:\n"},
	} {
		text, hits, err := s.read(context.Background(), readArgs{Path: tc.path, StartLine: tc.start, EndLine: tc.end})
		if read := len(hits) == 1 && hits[0].StartLine == tc.first && hits[0].EndLine == tc.last; err != nil || read != (tc.first > 0) || !strings.HasPrefix(text, tc.text) {
			t.Errorf("%s %d-%d after reads: %.80q %v", tc.path, tc.start, tc.end, text, err)
		}
	}
	// A tool that fails is told so, adds nothing, and may be called again.
	g.Retriever.Store.DB.Close()
	for range 2 {
		if text, grew := s.run(context.Background(), ai.ToolCall{ID: "x", Name: "users", Arguments: `{"name":"Handler"}`}, func(string, any) {}); text != "Error: the tool failed." || grew {
			t.Errorf("a failed tool: %q %v", text, grew)
		}
	}
}

// twinsFixture is a Gatherer asking model, on a snapshot whose twins are
// OrderPaid, OrderShipped, OrderClosed and OrderArchived: top-level records
// deriving from IntegrationEvent, which the snapshot declares once as a record
// (a test's copy aside), in Ordering and in Payment. Each is handled where the
// last is published: Payment's Worker.cs, Ordering's OrderPaidHandler.cs,
// Shipping's ShipHandler.cs, Ordering's CloseHandler.cs, then Payment's
// ArchiveHandler.cs, which publishes nothing. A pair deriving from
// an interface, a pair in tests, a pair whose base each project declares for
// itself, two copies in one project and classes within another are not twins.
func twinsFixture(t *testing.T, model string) (Gatherer, Request) {
	var fs []file
	var stmts []string
	declare := func(p, name, kind, sig string) {
		fs = append(fs, file{p, sig, [][3]any{{1, 1, name}}})
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature) VALUES (1, '%s', '%s', '%s', '%s', 1, 1, '%s')`, p, name, name, kind, sig))
	}
	use := func(p, content, imported string) {
		fs = append(fs, file{p, content, [][3]any{{1, 1, ""}}})
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, '%s', '%s')`, p, imported))
	}
	for _, p := range []string{"src/EventBus/EventBus.csproj", "src/Ordering/Ordering.csproj", "src/Payment/Payment.csproj", "tests/A/A.csproj", "tests/B/B.csproj"} {
		fs = append(fs, file{p, "<Project />", [][3]any{{1, 1, ""}}})
	}
	declare("src/EventBus/IntegrationEvent.cs", "IntegrationEvent", "record", "public record IntegrationEvent;")
	declare("tests/A/IntegrationEvent.cs", "IntegrationEvent", "record", "public record IntegrationEvent;")
	declare("src/EventBus/INotice.cs", "INotice", "interface", "public interface INotice")
	for _, project := range []string{"Ordering", "Payment"} {
		for _, name := range []string{"OrderPaid", "OrderShipped", "OrderClosed", "OrderArchived"} {
			declare("src/"+project+"/Events/"+name+".cs", name, "record", "public record "+name+"(int OrderId) : IntegrationEvent;")
		}
		declare("src/"+project+"/Events/Notice.cs", "Notice", "record", "public record Notice(string Text) : INotice;")
		declare("src/"+project+"/EventBase.cs", "EventBase", "class", "public abstract class EventBase")
		declare("src/"+project+"/Events/Shipped.cs", "Shipped", "class", "public class Shipped : EventBase")
	}
	for _, project := range []string{"A", "B"} {
		declare("tests/"+project+"/Probe.cs", "Probe", "record", "public record Probe : IntegrationEvent;")
	}
	for _, p := range []string{"src/Ordering/Copy.cs", "src/Ordering/Events/Copy.cs"} { // two of one project
		declare(p, "Copy", "record", "public record Copy : IntegrationEvent;")
	}
	for _, project := range []string{"Ordering", "Payment"} { // a class within another
		p := "src/" + project + "/Outer.cs"
		fs = append(fs, file{p, "public class Outer {\n  public class Inner : IntegrationEvent {}\n}", [][3]any{{1, 3, "Outer"}}})
		stmts = append(stmts, `INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line, signature) VALUES
			(1, '`+p+`', 'Outer', 'Outer', 'class', 1, 3, 'public class Outer {'), (1, '`+p+`', 'Inner', 'Outer.Inner', 'class', 2, 2, 'public class Inner : IntegrationEvent {}')`)
	}
	use("src/Ordering/Handlers/OrderPaidHandler.cs", "public Task Handle(OrderPaid paid) => bus.PublishAsync(new OrderShipped(paid.OrderId));", "src/Ordering/Events/OrderPaid.cs")
	use("src/Payment/Worker.cs", "await bus.PublishAsync(new OrderPaid(order.Id));", "src/Payment/Events/OrderPaid.cs")
	use("src/Shipping/ShipHandler.cs", "public Task Handle(OrderShipped s) => bus.PublishAsync(new OrderClosed(s.OrderId));", "src/Payment/Events/OrderShipped.cs")
	use("src/Ordering/CloseHandler.cs", "public Task Handle(OrderClosed c) => bus.PublishAsync(new OrderArchived(c.OrderId));", "src/Ordering/Events/OrderClosed.cs")
	use("src/Payment/ArchiveHandler.cs", "public Task Handle(OrderArchived a) => archive.Store(a.OrderId);", "src/Payment/Events/OrderArchived.cs")
	fs = append(fs, file{"src/Ordering/Ledger.cs", "public class Ledger { }", [][3]any{{1, 1, ""}}})
	return seed(t, model, fs, stmts...)
}

// TestTwins: users finds the code using a message in each project that
// declares a copy of it, and says the copies are one message when they are
// twins (twinsFixture); twins.Names are the names of those alone.
func TestTwins(t *testing.T) {
	g, req := twinsFixture(t, "http://127.0.0.1:1")
	s := &state{g: g, req: req, res: Result{Calls: map[string]int{}}}
	text, hits, err := s.usersOf(context.Background(), usersArgs{Name: "OrderPaid"})
	if want := []string{"src/Ordering/Handlers/OrderPaidHandler.cs:1-1", "src/Payment/Worker.cs:1-1"}; err != nil || !slices.Equal(paths(hits), want) ||
		!strings.Contains(text, "Note: OrderPaid is one message type, each project declaring its own copy deriving from IntegrationEvent: "+
			"src/Ordering/Events/OrderPaid.cs (project src/Ordering), src/Payment/Events/OrderPaid.cs (project src/Payment). Code using any copy") {
		t.Errorf("OrderPaid: %q %q %v", text, paths(hits), err)
	}
	for _, name := range []string{"Notice", "Probe", "Shipped", "Copy", "Inner"} {
		if text, _, err := s.usersOf(context.Background(), usersArgs{Name: name}); err != nil || strings.Contains(text, "Note:") {
			t.Errorf("%s: %q %v", name, text, err)
		}
	}
	ctx, store := context.Background(), g.Retriever.Store
	names, err := twins.Names(ctx, store, 1)
	if err != nil || !maps.Equal(names, map[string]bool{"OrderPaid": true, "OrderShipped": true, "OrderClosed": true, "OrderArchived": true}) {
		t.Errorf("message types %v %v", names, err)
	}
	all, err := store.Symbols(ctx, 1, "", 100) // every name declared: one is a message type exactly when it has twins
	if err != nil || len(all) < 20 {
		t.Fatalf("%d symbols: %v", len(all), err)
	}
	for _, sym := range all {
		if tw, err := twins.Of(ctx, store, 1, sym.Name); err != nil || names[sym.Name] != (len(tw) > 0) {
			t.Errorf("%s: a message type %v, twins %v %v", sym.Name, names[sym.Name], tw, err)
		}
	}
}

// chunkAt is the one chunk of g's snapshot at path p.
func chunkAt(t *testing.T, g Gatherer, p string) retrieval.Hit {
	var id int64
	err := g.Retriever.Store.DB.QueryRowContext(context.Background(), `SELECT id FROM chunks WHERE path = ?`, p).Scan(&id)
	hits, err2 := g.Retriever.Store.Chunks(context.Background(), []int64{id})
	if err != nil || err2 != nil || len(hits) != 1 {
		t.Fatal(p, err, err2)
	}
	return hits[0]
}

// TestFollow: the loop follows the message types the code found names, the
// first search's too, each once a gathering and three steps from a call: from
// Worker.cs (OrderPaid) to OrderPaid's users, then OrderShipped's, then
// OrderClosed's, CloseHandler.cs; OrderArchived, which that one publishes, only
// once a read finds it named. What it finds joins the evidence as users' finds
// do, before search_code's, with the tool "follow"; the model is told each
// type followed and the ranges found.
func TestFollow(t *testing.T) {
	ctx := context.Background()
	srv, asked := model(t, []call{{"search_code", `{"query":"Ledger","path_prefix":""}`}, {"read_file", `{"path":"src/Ordering/CloseHandler.cs","start_line":0,"end_line":0}`}})
	g, req := twinsFixture(t, srv.URL)
	worker := chunkAt(t, g, "src/Payment/Worker.cs")
	req.First = retrieval.Result{Hits: []retrieval.Hit{worker}}
	res := g.Gather(ctx, req, func(string, any) {})
	var from []string
	for _, o := range res.Origins {
		from = append(from, fmt.Sprint(o.Round, " ", o.Tool))
	}
	if want := []string{"src/Ordering/CloseHandler.cs:1-1", "src/Ordering/Handlers/OrderPaidHandler.cs:1-1", "src/Payment/Worker.cs:1-1", "src/Shipping/ShipHandler.cs:1-1", "src/Payment/ArchiveHandler.cs:1-1", "src/Ordering/Ledger.cs:1-1"}; !slices.Equal(paths(res.Hits), want) ||
		!slices.Equal(from, []string{"2 read_file", "1 follow", "1 first", "1 follow", "2 follow", "2 search_code"}) || !maps.Equal(res.Calls, map[string]int{"search_code": 1, "read_file": 1, "follow": 4}) || res.Graph != 5 || res.Stop != StopAnswered {
		t.Errorf("evidence %q from %q, calls %v, graph %d, stopped %q", paths(res.Hits), from, res.Calls, res.Graph, res.Stop)
	}
	if first := asked()[0][1]["content"].(string); !strings.HasSuffix(first, "Followed OrderPaid to: src/Ordering/Handlers/OrderPaidHandler.cs:1-1, src/Payment/Worker.cs:1-1.\n"+
		"Followed OrderShipped to: src/Shipping/ShipHandler.cs:1-1.\nFollowed OrderClosed to: src/Ordering/CloseHandler.cs:1-1.\n") {
		t.Errorf("first request %q", first)
	}
	if got := results(asked())["r0c1"]; !strings.HasSuffix(got, "OrderArchived(c.OrderId));\nFollowed OrderArchived to: src/Payment/ArchiveHandler.cs:1-1.\n") {
		t.Errorf("the read was told %q", got)
	}

	// A type no code uses, or whose users fail, is followed once and adds and tells nothing; a
	// gathering that can't read the message types follows none, and goes on.
	srv, _ = model(t)
	g, req = twinsFixture(t, srv.URL)
	req.First = retrieval.Result{Hits: []retrieval.Hit{worker}}
	s := start(g, req)
	s.messages = map[string]bool{"Notice": true, "OrderPaid": true}
	if note := s.follow(ctx, []retrieval.Hit{{ChunkID: worker.ChunkID, Content: "Notice"}}, follows); note != "" || len(s.users) != 0 || s.res.Calls["follow"] != 1 {
		t.Errorf("a type no code uses: told %q, users %q, calls %v", note, paths(s.users), s.res.Calls)
	}
	g.Retriever.Store.DB.Close()
	if note := s.follow(ctx, []retrieval.Hit{worker}, follows); note != "" || s.res.Calls["follow"] != 2 || !s.followed["OrderPaid"] {
		t.Errorf("a type whose users fail: told %q, calls %v", note, s.res.Calls)
	}
	if res = g.Gather(ctx, req, func(string, any) {}); res.Calls["follow"] != 0 || res.Stop != StopAnswered || !slices.Equal(paths(res.Hits), []string{"src/Payment/Worker.cs:1-1"}) {
		t.Errorf("no message types: calls %v, stopped %q, evidence %q", res.Calls, res.Stop, paths(res.Hits))
	}
}
