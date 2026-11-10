package loop

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
)

// chunk is a found chunk, id's: f<id>.go's first line, each the same size.
func chunk(id int64) retrieval.Hit {
	return retrieval.Hit{ChunkID: id, Path: fmt.Sprintf("f%02d.go", id), StartLine: 1, EndLine: 1, Content: "x"}
}

func chunks(ids ...int64) []retrieval.Hit {
	var out []retrieval.Hit
	for _, id := range ids {
		out = append(out, chunk(id))
	}
	return out
}

// outs names each hit kept out with the call that did it and where it was found.
func outs(list []Out) []string {
	var out []string
	for _, o := range list {
		out = append(out, fmt.Sprintf("%s:%d-%d r%d %s %s", o.Path, o.StartLine, o.EndLine, o.Round, o.Tool, o.From))
	}
	return out
}

// shown checks that evidence.Format shows all of s's evidence, whole, within
// its budget.
func shown(t *testing.T, s *state) []string {
	t.Helper()
	hits, _ := s.evidence()
	sources, used := evidence.Format(hits, s.req.Chars)
	if len(used) != len(hits) || len(sources) != size(hits) || size(hits) > s.req.Chars {
		t.Errorf("Format showed %d of %d hits, %d bytes of %d, budget %d", len(used), len(hits), len(sources), size(hits), s.req.Chars)
	}
	return paths(hits)
}

// TestEvictionOrder: a hit that does not fit evicts chunks other calls found,
// oldest call first, so the first search's first, and within a call the lowest
// ranked first; the call's result names each. A budget met exactly is not
// over, one byte short is. A chunk the first search found twice keeps the
// rank it was first found at.
func TestEvictionOrder(t *testing.T) {
	S := evidence.Size(chunk(1))
	s := start(Gatherer{}, Request{First: retrieval.Result{Hits: chunks(1, 2, 3)}, Limits: Limits{Rounds: 4, Chars: 7 * S}})
	s.res.Rounds = 1
	s.add("search_code", chunks(4, 5))
	s.add("users", chunks(6, 7))
	if got := shown(t, s); len(s.res.Evicted)+len(s.res.Refused) != 0 || len(got) != 7 {
		t.Fatalf("seven fit seven: evidence %q, evicted %q, refused %q", got, outs(s.res.Evicted), outs(s.res.Refused))
	}
	s.res.Rounds = 2
	grew, note := s.add("search_code", chunks(8, 9, 10, 11, 12, 13, 14))
	var want []string
	for _, e := range []struct {
		id   int64
		from string
	}{{3, "first"}, {2, "first"}, {1, "first"}, {5, "search_code"}, {4, "search_code"}, {7, "users"}, {6, "users"}} {
		want = append(want, fmt.Sprintf("f%02d.go:1-1 r3 search_code %s", e.id, e.from))
	}
	if !slices.Equal(outs(s.res.Evicted), want) || !grew || len(s.res.Refused) != 0 {
		t.Errorf("evicted %q, want %q; grew %v, refused %q", outs(s.res.Evicted), want, grew, outs(s.res.Refused))
	}
	if got := shown(t, s); !slices.Equal(got, paths(chunks(8, 9, 10, 11, 12, 13, 14))) {
		t.Errorf("evidence %q", got)
	}
	if want := "To make room, these left the evidence: f03.go:1-1, f02.go:1-1, f01.go:1-1, f05.go:1-1, f04.go:1-1, f07.go:1-1, f06.go:1-1. A later call may find them again.\n"; note != want {
		t.Errorf("told %q", note)
	}

	s = start(Gatherer{}, Request{First: retrieval.Result{Hits: chunks(1)}, Limits: Limits{Rounds: 4, Chars: 2*S - 1}})
	s.res.Rounds = 1
	s.add("search_code", chunks(2))
	if got := shown(t, s); !slices.Equal(got, paths(chunks(2))) || !slices.Equal(outs(s.res.Evicted), []string{"f01.go:1-1 r2 search_code first"}) {
		t.Errorf("one byte over: evidence %q, evicted %q", got, outs(s.res.Evicted))
	}

	s = start(Gatherer{}, Request{First: retrieval.Result{Hits: chunks(1, 2, 1)}, Limits: Limits{Rounds: 4, Chars: 2 * S}})
	s.res.Rounds = 1
	s.add("search_code", chunks(3))
	if want := []string{"f02.go:1-1 r2 search_code first"}; !slices.Equal(outs(s.res.Evicted), want) {
		t.Errorf("a chunk found twice: evicted %q, want %q", outs(s.res.Evicted), want)
	}
}

// TestRefusal: a hit that would not fit even with every chunk other calls
// found evicted is refused, nothing evicted for it, and told with its size and
// the bytes left; a call's own chunks are not evicted for it, nor are ranges
// read, ever. A range read that holds a chunk takes its place, needing no
// room. A chunk evicted may be found again, with the origin of the call that
// found it. A chunk within a range read is no new code.
func TestRefusal(t *testing.T) {
	S := evidence.Size(chunk(1))
	s := start(Gatherer{}, Request{Limits: Limits{Rounds: 4, Chars: S}})
	s.res.Rounds = 1
	grew, note := s.add("search_code", chunks(1, 2))
	if want := fmt.Sprintf("Not added, over the evidence budget: f02.go:1-1 (%d bytes); 0 bytes left of %d. Read a narrower range, or call done.\n", S, S); !grew || note != want ||
		!slices.Equal(shown(t, s), paths(chunks(1))) || !slices.Equal(outs(s.res.Refused), []string{"f02.go:1-1 r2 search_code "}) {
		t.Errorf("a call's second chunk: grew %v, told %q, refused %q", grew, note, outs(s.res.Refused))
	}

	s = start(Gatherer{}, Request{First: retrieval.Result{Hits: chunks(1, 2)}, Limits: Limits{Rounds: 4, Chars: 3 * S}})
	s.res.Rounds = 1
	read := retrieval.Hit{Path: "f99.go", StartLine: 1, EndLine: 1, Content: "x"}
	s.add("read_file", []retrieval.Hit{read})
	s.add("search_code", chunks(3))
	if got := shown(t, s); !slices.Equal(got, []string{"f99.go:1-1", "f03.go:1-1", "f01.go:1-1"}) || !slices.Equal(outs(s.res.Evicted), []string{"f02.go:1-1 r2 search_code first"}) {
		t.Fatalf("evidence %q, evicted %q", got, outs(s.res.Evicted))
	}
	big := retrieval.Hit{Path: "f98.go", StartLine: 1, EndLine: 12, Content: strings.TrimSuffix(strings.Repeat("x\n", 12), "\n")}
	if n := evidence.Size(big); n <= 2*S || n > 3*S {
		t.Fatalf("big is %d bytes: it must fit only with the range read evicted", n)
	}
	s.res.Rounds = 2
	grew, note = s.add("read_file", []retrieval.Hit{big})
	if want := fmt.Sprintf("Not added, over the evidence budget: f98.go:1-12 (%d bytes); 0 bytes left of %d. Read a narrower range, or call done.\n", evidence.Size(big), 3*S); grew || note != want ||
		len(s.res.Evicted) != 1 || !slices.Equal(outs(s.res.Refused), []string{"f98.go:1-12 r3 read_file "}) {
		t.Errorf("big: grew %v, told %q, evicted %q, refused %q", grew, note, outs(s.res.Evicted), outs(s.res.Refused))
	}
	if got := shown(t, s); !slices.Equal(got, []string{"f99.go:1-1", "f03.go:1-1", "f01.go:1-1"}) {
		t.Errorf("after big: %q", got)
	}

	grew, note = s.add("read_file", []retrieval.Hit{{Path: "f01.go", StartLine: 1, EndLine: 1, Content: "x"}})
	if got := shown(t, s); !grew || note != "" || len(s.res.Evicted) != 1 || !slices.Equal(got, []string{"f99.go:1-1", "f01.go:1-1", "f03.go:1-1"}) {
		t.Errorf("a read holding a chunk: grew %v, told %q, evicted %q, evidence %q", grew, note, outs(s.res.Evicted), got)
	}

	s.res.Rounds = 3
	grew, _ = s.add("users", chunks(2))
	hits, origins := s.evidence()
	if !grew || !slices.Equal(paths(hits), []string{"f99.go:1-1", "f01.go:1-1", "f02.go:1-1"}) || origins[2] != (Origin{4, "users", 1}) ||
		!slices.Equal(outs(s.res.Evicted), []string{"f02.go:1-1 r2 search_code first", "f03.go:1-1 r4 users search_code"}) {
		t.Errorf("found again: grew %v, evidence %q, origins %v, evicted %q", grew, paths(hits), origins, outs(s.res.Evicted))
	}

	s = start(Gatherer{}, Request{Limits: Limits{Rounds: 4, Chars: 3 * S}})
	s.add("read_file", []retrieval.Hit{{Path: "f05.go", StartLine: 1, EndLine: 1, Content: "x"}})
	if grew, _ := s.add("search_code", chunks(5)); grew || !slices.Equal(shown(t, s), []string{"f05.go:1-1"}) {
		t.Errorf("a chunk within a range read added new code: %v", grew)
	}
}

// TestFirstOverBudget: the first search's hits that do not fit are left out
// and named in the first request. That is a refusal told: a reply that then
// adds nothing, some of it refused, ends the gathering, but one that adds
// nothing with nothing refused does not, nor does one that adds code with
// some of it refused.
func TestFirstOverBudget(t *testing.T) {
	read := call{"read_file", `{"path":"src/tree.go","start_line":0,"end_line":0}`}
	srv, asked := model(t, []call{{"search_code", `{"query":"module","path_prefix":""}`}}, []call{read})
	g, req := fixture(t, srv.URL)
	req.Chars = evidence.Size(req.First.Hits[0]) + 5
	res := g.Gather(context.Background(), req, func(string, any) {})
	if want := []string{"src/handler.go:1-3 r1 first ", "src/tree.go:1-30 r3 read_file "}; res.Stop != StopBudget || res.Rounds != 2 ||
		!slices.Equal(outs(res.Refused), want) || !slices.Equal(paths(res.Hits), []string{"go.mod:1-1"}) {
		t.Errorf("stopped %q after %d rounds, refused %q, evidence %q", res.Stop, res.Rounds, outs(res.Refused), paths(res.Hits))
	}
	all := asked()
	if want := fmt.Sprintf("Not in the evidence, over its budget of %d bytes: src/handler.go:1-3 (%d bytes).\n", req.Chars, evidence.Size(req.First.Hits[1])); !strings.HasSuffix(all[0][1]["content"].(string), want) {
		t.Errorf("first request %q", all[0][1]["content"])
	}
	if got := results(all)["r0c0"]; strings.Contains(got, "Not added") {
		t.Errorf("a search adding nothing new was told %q", got)
	}

	mux := retrieval.Hit{Path: "src/mux.go", StartLine: 1, EndLine: 3, Content: "package src\n\nfunc (m *Mux) Handle(h Handler) { m.h = h }"}
	srv, _ = model(t, []call{{"read_file", `{"path":"src/mux.go","start_line":0,"end_line":0}`}, read})
	g, req = fixture(t, srv.URL)
	req.Chars = evidence.Size(req.First.Hits[0]) + evidence.Size(mux)
	res = g.Gather(context.Background(), req, func(string, any) {})
	if want := []string{"src/handler.go:1-3 r1 first ", "src/tree.go:1-30 r2 read_file "}; res.Stop != StopAnswered || res.Rounds != 2 ||
		!slices.Equal(outs(res.Refused), want) || !slices.Equal(paths(res.Hits), []string{"src/mux.go:1-3", "go.mod:1-1"}) {
		t.Errorf("a reply adding code, some refused: stopped %q after %d rounds, refused %q, evidence %q", res.Stop, res.Rounds, outs(res.Refused), paths(res.Hits))
	}
}

// TestFollowBudget: what follow finds (twinsFixture) goes through the budget
// as a call's finds do. With room for two of the first three chunks, following
// OrderShipped evicts the first search's hit, then following OrderClosed, the
// third step, evicts OrderPaid's handler, and the first request says so.
// With room for the first search's hit alone, OrderPaid's handler is refused,
// and code refused is not followed further. That refusal, told in the first
// request, is a refusal told: a reply that adds nothing, refused, ends the
// gathering.
func TestFollowBudget(t *testing.T) {
	ctx := context.Background()
	srv, asked := model(t)
	g, req := twinsFixture(t, srv.URL)
	worker, paid, ship := chunkAt(t, g, "src/Payment/Worker.cs"), chunkAt(t, g, "src/Ordering/Handlers/OrderPaidHandler.cs"), chunkAt(t, g, "src/Shipping/ShipHandler.cs")
	req.First, req.Chars = retrieval.Result{Hits: []retrieval.Hit{worker}}, size([]retrieval.Hit{worker, paid, ship})-1
	res := g.Gather(ctx, req, func(string, any) {})
	if first := asked()[0][1]["content"].(string); !strings.HasSuffix(first, "Followed OrderShipped to: src/Shipping/ShipHandler.cs:1-1.\n"+
		"To make room, these left the evidence: src/Payment/Worker.cs:1-1. A later call may find them again.\n"+
		"Followed OrderClosed to: src/Ordering/CloseHandler.cs:1-1.\n"+
		"To make room, these left the evidence: src/Ordering/Handlers/OrderPaidHandler.cs:1-1. A later call may find them again.\n") ||
		!slices.Equal(outs(res.Evicted), []string{"src/Payment/Worker.cs:1-1 r1 follow first", "src/Ordering/Handlers/OrderPaidHandler.cs:1-1 r1 follow follow"}) ||
		!slices.Equal(paths(res.Hits), []string{"src/Shipping/ShipHandler.cs:1-1", "src/Ordering/CloseHandler.cs:1-1"}) {
		t.Errorf("first request %q, evicted %q, evidence %q", first, outs(res.Evicted), paths(res.Hits))
	}

	srv, asked = model(t, []call{{"read_file", `{"path":"src/Shipping/ShipHandler.cs","start_line":0,"end_line":0}`}})
	g, req = twinsFixture(t, srv.URL)
	req.First, req.Chars = retrieval.Result{Hits: []retrieval.Hit{worker}}, size([]retrieval.Hit{worker})
	res = g.Gather(ctx, req, func(string, any) {})
	if first := asked()[0][1]["content"].(string); !strings.Contains(first, "Followed OrderPaid to: src/Ordering/Handlers/OrderPaidHandler.cs:1-1, src/Payment/Worker.cs:1-1.\n"+
		"Not added, over the evidence budget: src/Ordering/Handlers/OrderPaidHandler.cs:1-1 (") || res.Stop != StopBudget || res.Rounds != 1 ||
		!maps.Equal(res.Calls, map[string]int{"read_file": 1, "follow": 1}) || !slices.Equal(paths(res.Hits), []string{"src/Payment/Worker.cs:1-1"}) {
		t.Errorf("first request %q; stopped %q after %d rounds, calls %v, evidence %q", first, res.Stop, res.Rounds, res.Calls, paths(res.Hits))
	}
}

// TestFollowOut: code is followed no further than the type whose finds evict
// it: with room for OrderPaid's handler alone, following OrderPaid evicts the
// handler for Worker.cs, so OrderShipped, which the handler names next, is not
// followed. A reply that adds nothing, whose follow is refused some of what it
// finds, ends the gathering as its own refusal would: users finds
// CloseHandler.cs, which the first request's third step found and the budget
// held, and OrderArchived, which it names and no follow reached, has a second
// user too big to fit; its first evicts the oldest calls' finds.
func TestFollowOut(t *testing.T) {
	ctx := context.Background()
	srv, _ := model(t, []call{{"users", `{"name":"OrderClosed"}`}})
	g, req := twinsFixture(t, srv.URL)
	worker, paid, ship, closer := chunkAt(t, g, "src/Payment/Worker.cs"), chunkAt(t, g, "src/Ordering/Handlers/OrderPaidHandler.cs"), chunkAt(t, g, "src/Shipping/ShipHandler.cs"), chunkAt(t, g, "src/Ordering/CloseHandler.cs")
	req.First, req.Chars = retrieval.Result{Hits: []retrieval.Hit{paid}}, size([]retrieval.Hit{paid})
	s := start(g, req)
	s.messages = map[string]bool{"OrderPaid": true, "OrderShipped": true, "OrderClosed": true, "OrderArchived": true}
	if note := s.follow(ctx, []retrieval.Hit{paid}, follows); note != "Followed OrderPaid to: src/Ordering/Handlers/OrderPaidHandler.cs:1-1, src/Payment/Worker.cs:1-1.\n"+
		"To make room, these left the evidence: src/Ordering/Handlers/OrderPaidHandler.cs:1-1. A later call may find them again.\n" ||
		!maps.Equal(s.res.Calls, map[string]int{"follow": 1}) || !slices.Equal(shown(t, s), []string{"src/Payment/Worker.cs:1-1"}) {
		t.Errorf("told %q, calls %v, evidence %q", note, s.res.Calls, shown(t, s))
	}

	audit := `public Task Handle(OrderArchived a) => audit.Log(a.OrderId, "` + strings.Repeat("x", 2000) + `");`
	exec := func(q string, args ...any) {
		if _, err := g.Retriever.Store.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO file_contents(hash, content) VALUES ('audit', ?)`, audit)
	exec(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, 'src/Payment/Audit.cs', 'go', 'audit', ?, 1)`, len(audit))
	exec(`INSERT INTO chunks(snapshot_id, path, symbol_name, start_line, end_line, content, content_hash) VALUES (1, 'src/Payment/Audit.cs', '', 1, 1, ?, 'audit1')`, audit)
	exec(`INSERT INTO file_edges(snapshot_id, src, dst) VALUES (1, 'src/Payment/Audit.cs', 'src/Payment/Events/OrderArchived.cs')`)
	big := retrieval.Hit{ChunkID: -1, Path: "gen/big.cs", StartLine: 1, EndLine: 1, Content: strings.Repeat("x", 2000)}
	req.First, req.Chars = retrieval.Result{Hits: []retrieval.Hit{worker, big}}, size([]retrieval.Hit{worker, paid, ship, closer})
	res := g.Gather(ctx, req, func(string, any) {})
	if res.Stop != StopBudget || res.Rounds != 1 || !maps.Equal(res.Calls, map[string]int{"users": 1, "follow": 4}) ||
		!slices.Equal(outs(res.Evicted), []string{"src/Payment/Worker.cs:1-1 r2 follow first", "src/Ordering/Handlers/OrderPaidHandler.cs:1-1 r2 follow follow"}) ||
		!slices.Equal(outs(res.Refused), []string{"gen/big.cs:1-1 r1 first ", "src/Payment/Audit.cs:1-1 r2 follow "}) ||
		!slices.Equal(paths(res.Hits), []string{"src/Shipping/ShipHandler.cs:1-1", "src/Ordering/CloseHandler.cs:1-1", "src/Payment/ArchiveHandler.cs:1-1"}) {
		t.Errorf("stopped %q after %d rounds, calls %v, evicted %q, refused %q, evidence %q", res.Stop, res.Rounds, res.Calls, outs(res.Evicted), outs(res.Refused), paths(res.Hits))
	}
}
