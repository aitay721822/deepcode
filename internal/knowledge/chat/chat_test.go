package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/chat/loop"
	"deepcode/internal/knowledge/chat/store"
	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/jobs"
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	catalogstore "deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graph/store"
)

// TestAskLimit: a user has at most maxAsking answers written at a time, which
// holds up nobody else, and a finished Ask gives its place back. The asker is
// told why.
func TestAskLimit(t *testing.T) {
	var s Service // no model: an Ask let through fails with errNoModel
	ask := func(user int64) error {
		return s.Ask(context.Background(), Request{UserID: user, Question: "why?"}, nil)
	}
	for range maxAsking {
		s.begin(1)
	}
	if err := ask(1); !errors.Is(err, errBusy) {
		t.Fatalf("one answer too many: %v", err)
	}
	if msg, logIt := Explain(errBusy); msg != errBusy.Error() || logIt {
		t.Fatalf("the asker is told %q (logged: %v)", msg, logIt)
	}
	if err := ask(2); !errors.Is(err, errNoModel) {
		t.Fatalf("another user: %v", err)
	}
	s.end(1)
	for range 2 { // the first gives its place back, so the second gets it
		if err := ask(1); !errors.Is(err, errNoModel) {
			t.Fatalf("after one finished: %v", err)
		}
	}
	// A question is 1 to 4000 characters, counted as characters, not bytes.
	for q, want := range map[string]error{" \n": errQuestion, strings.Repeat("長", maxQuestion): errNoModel, strings.Repeat("長", maxQuestion+1): errQuestion} {
		if err := s.Ask(context.Background(), Request{UserID: 3, Question: q}, nil); !errors.Is(err, want) {
			t.Fatalf("a question of %d characters: %v", len([]rune(q)), err)
		}
	}
	// Other errors may quote SQL or a server's answer: the asker gets a generic line, the log the error.
	if msg, logIt := Explain(errors.New(`near "SELECT": syntax error`)); strings.Contains(msg, "SELECT") || !logIt {
		t.Fatalf("an unexplained error is told as %q (logged: %v)", msg, logIt)
	}
	if msg, logIt := Explain(fmt.Errorf("%w: %w", errModel, errors.New("502 from upstream"))); msg != errModel.Error() || !logIt {
		t.Fatalf("a model failure is told as %q (logged: %v)", msg, logIt)
	}
}

// service is the chat service on db, asking model (nil: none).
func service(db *sql.DB, model *ai.OpenAI) *Service {
	return &Service{Store: &store.Store{DB: db}, Chat: model,
		Catalog:   &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: &jobs.Queue{Store: &jobstore.Store{DB: db}}},
		Retriever: &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}}}}
}

// TestAnswers: an answer is written from the evidence, with the model's
// <think> block dropped; a follow-up is searched together with the previous
// question and sent after the turns before it; a question no code matches is
// answered so without asking the model for an answer.
func TestAnswers(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO users(id, username, password_hash) VALUES (1, 'a', 'x')`,
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`,
		`UPDATE repositories SET ready_snapshot_id = 1`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'src/store.ts', 1, 9, 'const subscribe = (listener) => listeners.add(listener)', 'h1')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var asked []string // what the model was asked for, in order
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ai.Message `json:"messages"`
			Tools    []any        `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) > 0 {
			gathered(w, "")
			return
		}
		reply := "nothing"
		if strings.HasPrefix(body.Messages[0].Content, "You are deepcode") {
			asked = append(asked, fmt.Sprintf("an answer, with %d messages", len(body.Messages)))
			reply = "<think>hidden</think>It subscribes [src/store.ts:1-2]."
		} else {
			asked = append(asked, "search terms")
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	s := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m"})
	ask := func(conv int64, q string) (done Message) {
		err := s.Ask(ctx, Request{RepoID: 1, ConversationID: conv, Question: q, UserID: 1}, func(event string, data any) {
			if event == "done" {
				done = data.(Message)
			}
		})
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return done
	}
	if m := ask(0, "subscribe"); !strings.HasPrefix(m.Content, "It subscribes") || len(m.Citations) != 1 {
		t.Fatalf("an answer: %+v", m)
	}
	if m := ask(1, "and then?"); !strings.HasPrefix(m.Content, "It subscribes") { // in conversation 1, just started
		t.Fatalf("a follow-up: %+v", m)
	}
	if m := ask(0, "zzzz qqqq"); m.Content != noEvidence || len(m.Citations) != 0 {
		t.Fatalf("no evidence: %+v", m)
	}
	if want := []string{"an answer, with 2 messages", "an answer, with 4 messages", "search terms"}; !slices.Equal(asked, want) {
		t.Fatalf("the model was asked for %q, want %q", asked, want)
	}
}

// gathered answers a request of the loop that gathers the evidence, whole:
// with a call of search_code for query, or ("") with none, which ends it.
// The text says what only the model may read.
func gathered(w http.ResponseWriter, query string) {
	msg := map[string]any{"role": "assistant", "content": "MARKER: let me think about where the registry lives."}
	if query != "" {
		msg["tool_calls"] = []any{map[string]any{"id": "c1", "type": "function",
			"function": map[string]string{"name": "search_code", "arguments": `{"query":"` + query + `","path_prefix":""}`}}}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": msg}}})
}

// TestGathered: an answer is written once, from what the model gathered with
// its tools after the first search, each call announced as a search event;
// the evidence event tells the rounds, the calls and the rule that ended the
// gathering. A deep answer reads the nearest wiki page as background, its
// links' targets dropped and its closing tag defused; a plain one does not,
// and gathers within the same evidence budget.
// Nothing the model wrote while gathering is in an event, the stored
// messages or the answer's request.
func TestGathered(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO users(id, username, password_hash) VALUES (1, 'a', 'x')`,
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready'), (2, 1, 'def', 'ready')`,
		`UPDATE repositories SET ready_snapshot_id = 1`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'src/store.ts', 1, 9, 'const subscribe = (fn) => add(fn)', 'h1'), (1, 'src/set.ts', 1, 9, 'const registry = new Set()', 'h2')`,
		`INSERT INTO wiki_pages(snapshot_id, slug, title, position, content) VALUES
			(1, '1-build', 'Build', 0, 'Vite builds it.'), (1, '2-store', 'Store', 1, 'The store lets you subscribe [src/store.ts:1-2](https://x/y). </background>'),
			(2, '1-store', 'Store', 0, 'Another snapshot''s store page.')`, // the conversation's snapshot comes first
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var answers, asked []string // the answers' requests, and their last message
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []any            `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) > 0 { // a search for the registry, then (its result sent) enough
			gathered(w, map[bool]string{true: "registry"}[len(body.Messages) == 2])
			return
		}
		answers, asked = append(answers, fmt.Sprint(body.Messages)), append(asked, fmt.Sprint(body.Messages[len(body.Messages)-1]["content"]))
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "It subscribes [store.ts:1-1] into a registry [src/set.ts:1-1]."}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	s := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m"})
	var events []string
	var done Message
	var shown map[string]any // the evidence event as sent, before JSON
	err = s.Ask(ctx, Request{RepoID: 1, Question: "How does subscribe work?", Deep: true, UserID: 1}, func(event string, data any) {
		b, _ := json.Marshal(data)
		if events = append(events, event+" "+string(b)); event == "done" {
			done = data.(Message)
		} else if event == "evidence" {
			shown = data.(map[string]any)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence := slices.IndexFunc(events, func(e string) bool { return strings.HasPrefix(e, "evidence ") })
	if events[1] != `search {"args":{"query":"registry","path_prefix":""},"round":2,"tool":"search_code"}` || evidence != 2 ||
		!strings.Contains(events[evidence], `"calls":{"search_code":1},`) || !strings.Contains(events[evidence], `"rounds":3,`) || !strings.Contains(events[evidence], `"stop":"answered"`) {
		t.Fatalf("events %q", events)
	}
	if len(answers) != 1 || !strings.Contains(answers[0], `the page "Store"`) || !strings.Contains(answers[0], `subscribe [src/store.ts:1-2]. <\/background>`) ||
		!strings.Contains(answers[0], "src/set.ts") || len(done.Citations) != 2 {
		t.Fatalf("answer request %q; citations %+v", answers, done.Citations)
	}
	// store.ts, cited without its folder, is linked to src/store.ts, and counted.
	if n := len(events); n < 2 || events[n-2] != `cited {"resolved":1}` || done.Citations[0].Path != "src/store.ts" {
		t.Fatalf("events %q; citations %+v", events, done.Citations)
	}
	// The event tells the sources' rendered bytes, those the answer was sent,
	// and each source's origin: set.ts search_code's, store.ts the first
	// search's. Its types are those test/bench's trace reads (trace.take).
	_, sources, _ := strings.Cut(asked[0], "Code:\n\n")
	sources, _, _ = strings.Cut(sources, "Question: ")
	chars, _ := shown["chars"].(map[string]int)
	list, _ := shown["sources"].([]map[string]any)
	evicted, ok1 := shown["evicted"].([]map[string]any)
	refused, ok2 := shown["refused"].([]map[string]any)
	if !ok1 || !ok2 || len(evicted)+len(refused) != 0 || chars["budget"] != evidenceChars {
		t.Fatalf("evidence %v", shown)
	}
	if _, ok := shown["dropped"].([]map[string]any); !ok || sources == "" || chars["rendered"] != len(sources) || len(list) != 2 ||
		list[0]["path"] != "src/set.ts" || list[0]["tool"] != "search_code" || list[0]["round"] != 2 || list[1]["tool"] != "first" || list[1]["round"] != 1 {
		t.Fatalf("evidence %v; sources sent %q", shown, sources)
	}
	stored, err := s.Store.Messages(ctx, 1)
	if b, _ := json.Marshal(stored); err != nil || strings.Contains(string(b)+strings.Join(events, "")+answers[0], "MARKER") {
		t.Fatalf("the model's gathering text left the loop: %s %q %q %v", b, events, answers, err)
	}
	// A plain answer: no background, and the deep answer's evidence budget.
	var plain map[string]int
	if err := s.Ask(ctx, Request{RepoID: 1, Question: "How does subscribe work?", UserID: 1}, func(event string, data any) {
		if event == "evidence" {
			plain, _ = data.(map[string]any)["chars"].(map[string]int)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(answers) != 2 || strings.Contains(answers[1], "background") || !strings.Contains(answers[1], "src/set.ts") || plain["budget"] != evidenceChars {
		t.Fatalf("a plain answer's request %q, chars %v", answers[1], plain)
	}
}

// TestEvidenceEvent: the evidence event names each source block with where it
// came from; the hits gathered that Format left out, in the order the answer
// would have read them, with their bytes; the hits the gathering's budget
// evicted, with where they had been found, and refused, each with the call
// that did it; and the rendered bytes against the budget.
func TestEvidenceEvent(t *testing.T) {
	got := loop.Result{Result: retrieval.Result{Hits: []retrieval.Hit{
		{Path: "a.go", StartLine: 1, EndLine: 2, Content: "x\ny"},
		{Path: "b.go", StartLine: 3, EndLine: 3, Symbol: "B", Content: strings.Repeat("z", 100)},
		{Path: "c.go", StartLine: 5, EndLine: 6, Content: "w"},
	}}, Origins: []loop.Origin{{Round: 2, Tool: "read_file", Chars: 3}, {Round: 1, Tool: "first", Chars: 100}, {Round: 3, Tool: "users", Chars: 1}},
		Evicted: []loop.Out{{Hit: retrieval.Hit{Path: "d.go", StartLine: 1, EndLine: 4, Symbol: "D"}, Round: 3, Tool: "users", From: "first"}},
		Refused: []loop.Out{{Hit: retrieval.Hit{Path: "e.go", StartLine: 7, EndLine: 9}, Round: 2, Tool: "read_file"}},
		Rounds:  2, Calls: map[string]int{"read_file": 1}, Stop: "rounds"}
	sources, used := evidence.Format(got.Hits, 80) // a.go's block fits, b.go's does not
	b, _ := json.Marshal(evidenceEvent(got, used, len(sources), 80))
	want := fmt.Sprintf(`{"calls":{"read_file":1},"chars":{"budget":80,"rendered":%d},`+
		`"dropped":[{"chars":100,"endLine":3,"path":"b.go","round":1,"startLine":3,"symbol":"B","tool":"first"},{"chars":1,"endLine":6,"path":"c.go","round":3,"startLine":5,"symbol":"","tool":"users"}],`+
		`"evicted":[{"endLine":4,"from":"first","path":"d.go","round":3,"startLine":1,"symbol":"D","tool":"users"}],`+
		`"fts":0,"graph":0,"named":0,`+
		`"refused":[{"endLine":9,"path":"e.go","round":2,"startLine":7,"symbol":"","tool":"read_file"}],`+
		`"rounds":3,"sources":[{"endLine":2,"path":"a.go","round":2,"startLine":1,"symbol":"","tool":"read_file"}],"stop":"rounds","symbol":0,"vector":0}`, len(sources))
	if len(used) != 1 || string(b) != want {
		t.Errorf("%s\nwant %s", b, want)
	}
	all := `"sources":[{"endLine":2,"path":"a.go","round":2,"startLine":1,"symbol":"","tool":"read_file"},` +
		`{"endLine":3,"path":"b.go","round":1,"startLine":3,"symbol":"B","tool":"first"},{"endLine":6,"path":"c.go","round":3,"startLine":5,"symbol":"","tool":"users"}]`
	got.Evicted, got.Refused = nil, nil
	if b, _ := json.Marshal(evidenceEvent(got, got.Hits, 9, 80)); !strings.Contains(string(b), `"dropped":[]`) || !strings.Contains(string(b), all) ||
		!strings.Contains(string(b), `"evicted":[]`) || !strings.Contains(string(b), `"refused":[]`) {
		t.Errorf("nothing left out: %s", b)
	}
}

// TestNearest: the page sharing most of the question's words wins, a title
// counting thrice and Chinese read in pairs of characters; a word all pages
// hold decides nothing, and no shared word means no page.
func TestNearest(t *testing.T) {
	titles := []string{"Overview", "路由與中介層", "Store"}
	contents := []string{"The router and the store.", "How the router matches paths.", "The store keeps data."}
	for q, want := range map[string]int{"How does the router work?": 1, "講解路由": 1, "where is the store": 2, "the the": -1, "zzz": -1} {
		if got := nearest(q, titles, contents); got != want {
			t.Errorf("%q: page %d, want %d", q, got, want)
		}
	}
	if got := wordsOf("講解路由 the Router 元件x"); !slices.Equal(slices.Sorted(maps.Keys(got)), []string{"router", "the", "元件", "解路", "講解", "路由"}) {
		t.Errorf("words %v", got)
	}
}

// TestOpenOwnConversation: a conversation is continued only by its owner and
// only in its repository; to anyone else it does not exist.
func TestOpenOwnConversation(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO users(id, username, password_hash) VALUES (1, 'a', 'x'), (2, 'b', 'x')`,
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r'), (2, 'https://github.com/o/s', 'o', 's')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'a', 'ready')`,
		`INSERT INTO conversations(id, repository_id, snapshot_id, title, user_id) VALUES (1, 1, 1, 't', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s := service(db, nil)
	at := catalog.Ref{ID: 1}
	if b, err := s.open(ctx, Request{UserID: 1, RepoID: 1, ConversationID: 1}, at, "q"); err != nil || b.id != 1 {
		t.Fatalf("the owner: %+v %v", b, err)
	}
	for _, req := range []Request{{UserID: 2, RepoID: 1, ConversationID: 1}, {UserID: 1, RepoID: 2, ConversationID: 1}} {
		if _, err := s.open(ctx, req, at, "q"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%+v: %v", req, err)
		}
	}
	// The history keeps its first message while it grows to 11, then drops 6 at once.
	for n, first := range map[int]string{0: "", 5: "m1", 11: "m1", 12: "m7", 17: "m7"} {
		db.ExecContext(ctx, `DELETE FROM messages`)
		for i := 1; i <= n; i++ {
			s.Store.AddMessage(ctx, 1, "user", fmt.Sprintf("m%d", i), nil)
		}
		h, err := s.history(ctx, 1)
		if got := ""; err != nil || len(h) > 0 && h[0].Content != first || len(h) == 0 && first != "" || len(h) > 0 && h[len(h)-1].Content != fmt.Sprintf("m%d", n) {
			if len(h) > 0 {
				got = h[0].Content
			}
			t.Errorf("%d messages: history starts at %q, want %q (%v)", n, got, first, err)
		}
	}
}

// TestLanguage: the prompt names the question's language last, whatever the
// sources are written in: Han makes it Chinese however much English code it
// names, quoted strings say nothing, English words make it English, and a
// question whose words tell nothing follows the last that did.
func TestLanguage(t *testing.T) {
	for q, want := range map[string]string{"How does Mount work?": "English", "Mount 如何運作？": chinese,
		"Mount はどう動きますか": "Japanese", "Как работает Mount?": own, "마운트는 어떻게 동작하나요": own, "？": own,
		"Wie funktioniert der Router?": own, "Is the café example in the docs current?": "English",
		"What does the 「錯誤」 message in handler.go mean?": "English", "What does `無法從目前程式碼確認` mean?": "English",
		"What is 路由?": chinese, // unquoted: the known limit
		"為何 PaymentSucceededEventHandler 會觸發 InvoiceProjection": chinese, "src/app/server.go 的 Route 在哪裡": chinese,
		"請看這個錯誤 panic: runtime error: index out of range [3] with length 3 goroutine 1 running main.go 怎麼修":      chinese,
		"請解釋 the router matching algorithm in detail please":                                                     chinese,
		"這段為何失敗：```go\nfunc (r *Router) Mount(pattern string, h http.Handler) { r.mux.Handle(pattern, h) }\n```": chinese,
	} {
		if got := prompt("go-chi/chi", "abc", nil, "", "", q).Dynamic; !strings.HasSuffix(got, "Question: "+q+"\n\n"+namesLine+" Answer in "+want+".") {
			t.Errorf("%q: %q", q, got)
		}
	}
	history := []ai.Message{{Role: "user", Content: "Mount 如何運作？"}, {Role: "assistant", Content: "It mounts a router."}}
	for _, q := range []string{"？", "Route?", "src/a/main.go?", "x_on_y?"} {
		if p := prompt("go-chi/chi", "abc", history, "", "", q); p.Static != systemPrompt || len(p.Previous) != 2 || p.Previous[0] != history[0] ||
			!strings.HasSuffix(p.Dynamic, "Answer in "+chinese+".") {
			t.Errorf("a follow-up whose words tell nothing: %q", p)
		}
	}
	for _, q := range []string{"Do we validate tokens?", "Isn't Route mounted?"} { // English, though the conversation was not
		if got := answerLanguage(q, history); got != "English" {
			t.Errorf("%q: %s", q, got)
		}
	}
	if uncited(answerLanguage("Route?", history)) != uncited(chinese) || uncited("English") == uncited(chinese) {
		t.Error("the footer is not in the answer's language")
	}
}

// TestCodemap: a code map is drawn with drawing events, its locations linked
// (a path's brackets escaped) and cited, and stored to be read back; an answer
// that is no map is told as errSchema, and logged; a map that shows no line of
// the sources, twice, as errMap.
func TestCodemap(t *testing.T) {
	ctx := context.Background()
	db, _ := storage.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	defer db.Close()
	db.Exec(`INSERT INTO users(id, username, password_hash) VALUES (1, 'a', 'x');
		INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready');
		UPDATE repositories SET ready_snapshot_id = 1;
		INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'app/[slug]/page.tsx', 1, 1, 'export default function Page() {}', 'h')`)
	drawn := `{"title":"Page","description":"` + strings.Repeat("x", 600) + `","traces":[{"title":"Render","description":"d","guide":"g","locations":[{"title":"Page","description":"renders",` +
		`"path":"app/[slug]/page.tsx","line":1,"code":"export default function Page() {}"}]}]}`
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ai.Message
			Tools    []any
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) > 0 {
			gathered(w, "")
			return
		}
		reply := drawn
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	s := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m"})
	var events []string
	var done Message
	err := s.Ask(ctx, Request{RepoID: 1, Question: "How is a Page rendered?", Codemap: true, UserID: 1}, func(event string, data any) {
		if events = append(events, event); event == "done" {
			done = data.(Message)
		}
	})
	stored, _ := s.Store.Messages(ctx, 1)
	if err != nil || !slices.Contains(events, "drawing") || !strings.Contains(done.Content, `- **1a** Page [app/\[slug\]/page.tsx:1-1](`) ||
		len(done.Citations) != 1 || len(stored) != 2 || !strings.Contains(string(stored[1].Codemap), `"id":"1a"`) {
		t.Fatalf("%v %v %+v %+v", err, events, done, stored)
	}
	for reply, want := range map[string]error{"no map": errSchema, strings.Replace(drawn, `"path":"app/[slug]/page.tsx"`, `"path":"nope.tsx"`, 1): errMap} {
		drawn = reply
		err := s.Ask(ctx, Request{RepoID: 1, Question: "How is a Page rendered?", Codemap: true, UserID: 1}, func(string, any) {})
		if msg, logIt := Explain(err); !errors.Is(err, want) || msg != want.Error() || logIt != (want == errSchema) {
			t.Fatalf("%.20q: %v, told %q", reply, err, msg)
		}
	}
}
