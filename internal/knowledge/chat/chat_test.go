package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/chat/harness"
	"deepcode/internal/knowledge/chat/loop"
	"deepcode/internal/knowledge/chat/store"
	"deepcode/internal/knowledge/codemap"
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
	graphstore "deepcode/internal/repo/index/graphstore"
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

var tokens, _ = harness.LoadTokens("") // OpenAI's o200k_base, the built-in
// service is the chat service on db, asking model (nil: none), counting its tokens by tokens.
func service(db *sql.DB, model *ai.OpenAI) *Service {
	return &Service{Store: &store.Store{DB: db}, Chat: model, Tokens: tokens,
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
	asked, refuse := []string{}, 0 // what the model was asked for, in order; answers to refuse as too long, as llama.cpp does
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
			if refuse > 0 {
				refuse--
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"code":400,"message":"too long","type":"exceed_context_size_error","n_prompt_tokens":1000,"n_ctx":100000}}`)
				return
			}
			_, lang, _ := strings.Cut(body.Messages[len(body.Messages)-1].Content, " Answer in ") // the language line, last
			asked = append(asked, fmt.Sprintf("an answer in %s with %d messages", lang, len(body.Messages)))
			reply = "<think>hidden</think>It subscribes [src/store.ts:1-2]."
		} else if body.Messages[0].Content == languagePrompt {
			asked, reply = append(asked, "its language"), "German"
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
	if want := []string{"its language", "an answer in German. with 2 messages", "its language", "an answer in German. with 4 messages", "its language", "search terms"}; !slices.Equal(asked, want) { // none in kana or Han
		t.Fatalf("the model was asked for %q, want %q", asked, want)
	}
	refuse = 1 // with the counts: the context learned, the request fitted again and sent once more
	if m := ask(0, "subscribe"); !strings.HasPrefix(m.Content, "It subscribes") || s.Chat.Context() != 100000 || refuse != 0 {
		t.Fatalf("refused once: %+v, context %d", m, s.Chat.Context())
	}
	refuse = 2 // twice: the asker is told to set the context
	if msg, logIt := Explain(s.Ask(ctx, Request{RepoID: 1, Question: "subscribe", UserID: 1}, func(string, any) {})); msg != errContext.Error() || !logIt || refuse != 0 {
		t.Fatalf("refused twice: %s", msg)
	}
	if msg, _ := Explain(service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m", Window: 9000}).Ask(ctx, Request{RepoID: 1, Question: "subscribe", UserID: 1}, func(string, any) {})); !strings.Contains(msg, "DEEPCODE_CHAT_CONTEXT 都要至少") {
		t.Fatalf("a context of 808 tokens past the answer's room: %s", msg)
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

// TestGathered: an answer is written once, from what the model gathered with its tools after the first
// search, each call announced as a search event; the evidence event tells the rounds, the calls and the rule
// that ended the gathering. A deep answer reads the nearest wiki page as background, its links' targets
// dropped and its closing tag defused, and with the model's context known gathers past the fixed evidence
// budget, as large a file as the room holds; a plain one does not, nor a deep one with no context known, nor
// one whose small context leaves too few tokens. Nothing the model wrote while gathering is in an event, the
// stored messages or the answer's request.
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
			(1, 'src/store.ts', 1, 9, 'const subscribe = (fn) => add(fn)', 'h1'), (1, 'src/set.ts', 1, 9, 'const registry = new Set()', 'h2'),
			(1, 'src/big.ts', 1, 9, 'const registry = "' || hex(zeroblob(33000)) || '"', 'h3')`, // 66,019 bytes: past evidenceChars
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
		reply := "English"
		if body.Messages[0]["content"] != languagePrompt {
			answers, asked = append(answers, fmt.Sprint(body.Messages)), append(asked, fmt.Sprint(body.Messages[len(body.Messages)-1]["content"]))
			reply = "It subscribes [store.ts:1-1] into a registry [src/set.ts:1-1]."
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	s := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m", Window: 524288})
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
		t.Fatalf("%d answer requests; citations %+v", len(answers), done.Citations)
	}
	// store.ts, cited without its folder, is linked to src/store.ts, and counted.
	if n := len(events); n < 2 || events[n-2] != `cited {"resolved":1}` || done.Citations[0].Path != "src/store.ts" {
		t.Fatalf("events %q; citations %+v", events, done.Citations)
	}
	// The event tells the sources' rendered bytes, those the answer was sent,
	// and each source's origin: big.ts and set.ts search_code's, store.ts the first
	// search's. Its types are those test/bench's trace reads (trace.take).
	_, sources, _ := strings.Cut(asked[0], "Code:\n\n")
	sources, _, _ = strings.Cut(sources, "Question: ")
	chars, _ := shown["chars"].(map[string]int)
	list, _ := shown["sources"].([]map[string]any)
	evicted, ok1 := shown["evicted"].([]map[string]any)
	refused, ok2 := shown["refused"].([]map[string]any)
	if _, bounded := chars["budget"]; !ok1 || !ok2 || len(evicted)+len(refused) != 0 || bounded || shown["window"].(map[string]int)["evidence"] <= evidenceChars {
		t.Fatalf("evidence %v %v %v", chars, evicted, refused)
	}
	if _, ok := shown["dropped"].([]map[string]any); !ok || sources == "" || chars["rendered"] != len(sources) || len(list) != 3 ||
		list[0]["path"] != "src/big.ts" || list[0]["tool"] != "search_code" || list[0]["round"] != 2 || list[1]["path"] != "src/set.ts" || list[2]["tool"] != "first" || list[2]["round"] != 1 {
		t.Fatalf("evidence %v; %d bytes of sources sent", shown["sources"], len(sources))
	}
	stored, err := s.Store.Messages(ctx, 1)
	if b, _ := json.Marshal(stored); err != nil || strings.Contains(string(b)+strings.Join(events, "")+answers[0], "MARKER") {
		t.Fatalf("the model's gathering text left the loop: %s %q %q %v", b, events, answers, err)
	}
	// A plain answer: no background, and the fixed evidence budget.
	var plain map[string]int
	if err := s.Ask(ctx, Request{RepoID: 1, Question: "How does subscribe work?", UserID: 1}, func(event string, data any) {
		if event == "evidence" {
			plain, _ = data.(map[string]any)["chars"].(map[string]int)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(answers) != 2 || strings.Contains(answers[1], "background") || !strings.Contains(answers[1], "src/set.ts") || plain["budget"] != evidenceChars {
		t.Fatalf("%d answer requests, chars %v", len(answers), plain)
	}
	for _, window := range []int{0, 20000} { // a deep answer with no context known: the fixed budget, the big file refused; with a small one, past its tokens, and no room for the page
		var deep map[string]any
		if err := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m", Window: window}).Ask(ctx, Request{RepoID: 1, Question: "How does subscribe work?", Deep: true, UserID: 1}, func(event string, data any) {
			if event == "evidence" {
				deep = data.(map[string]any)
			}
		}); err != nil || deep["chars"].(map[string]int)["budget"] != map[bool]int{true: evidenceChars}[window == 0] || len(deep["refused"].([]map[string]any)) != 1 || window == 0 && !strings.Contains(answers[len(answers)-1], `the page "Store"`) {
			t.Fatalf("deep, context %d: %v %v %v", window, err, deep["chars"], deep["refused"])
		}
	}
}

// TestConverges: a plain answer's gathering ends on a reply that only reads a file the first search found
// (loop.StopConverged); a deep answer's goes on, reading it again, until it stalls.
func TestConverges(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO users(id, username, password_hash) VALUES (1, 'a', 'x')`,
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`,
		`UPDATE repositories SET ready_snapshot_id = 1`,
		`INSERT INTO file_contents(hash, content) VALUES ('h1', 'const subscribe = (fn) => add(fn)')`,
		`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, 'src/store.ts', 'typescript', 'h1', 33, 1)`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'src/store.ts', 1, 1, 'const subscribe = (fn) => add(fn)', 'h1')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Tools []any }
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) > 0 { // every reply reads store.ts, which the first search found
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant",
				"tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]string{"name": "read_file", "arguments": `{"path":"src/store.ts","start_line":0,"end_line":0}`}}}}}}})
			return
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "It subscribes [src/store.ts:1-1]."}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	for deep, want := range map[bool]string{false: loop.StopConverged, true: loop.StopStalled} {
		var stop any
		err := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m"}).Ask(ctx, Request{RepoID: 1, Question: "How does subscribe work?", Deep: deep, UserID: 1}, func(event string, data any) {
			if event == "evidence" {
				stop = data.(map[string]any)["stop"]
			}
		})
		if err != nil || stop != want {
			t.Errorf("deep %v: stopped %v, want %s (%v)", deep, stop, want, err)
		}
	}
}

// TestEvidenceEvent: the evidence event names each source block with where it came from; the hits
// gathered that Format left out, in the order the answer would have read them, with their bytes; the
// hits the gathering's budget evicted, with where they had been found, and refused, each with the call
// that did it; the rendered bytes against the budget, if bytes bound it; and the context, its
// threshold, the parts' shares and the results masked.
func TestEvidenceEvent(t *testing.T) {
	got := loop.Result{Result: retrieval.Result{Hits: []retrieval.Hit{
		{Path: "a.go", StartLine: 1, EndLine: 2, Content: "x\ny"},
		{Path: "b.go", StartLine: 3, EndLine: 3, Symbol: "B", Content: strings.Repeat("z", 100)},
		{Path: "c.go", StartLine: 5, EndLine: 6, Content: "w"},
	}}, Origins: []loop.Origin{{Round: 2, Tool: "read_file", Chars: 3}, {Round: 1, Tool: "first", Chars: 100}, {Round: 3, Tool: "users", Chars: 1}},
		Evicted: []loop.Out{{Hit: retrieval.Hit{Path: "d.go", StartLine: 1, EndLine: 4, Symbol: "D"}, Round: 3, Tool: "users", From: "first"}},
		Refused: []loop.Out{{Hit: retrieval.Hit{Path: "e.go", StartLine: 7, EndLine: 9}, Round: 2, Tool: "read_file"}},
		Rounds:  2, Calls: map[string]int{"read_file": 1}, Stop: "rounds"}
	sources, used, _ := evidence.Format(got.Hits, evidence.Limit{Chars: 80}) // a.go's block fits, b.go's does not
	parts, fit := fitted{shares: harness.Shares{Evidence: 80, History: 10, Background: 5}}, harness.New(&ai.OpenAI{Window: 524288}, tokens)
	fit.Masked = 2
	b, _ := json.Marshal(evidenceEvent(got, used, len(sources), evidence.Limit{Chars: 80}, parts, fit))
	want := fmt.Sprintf(`{"calls":{"read_file":1},"chars":{"budget":80,"rendered":%d},`+
		`"dropped":[{"chars":100,"endLine":3,"path":"b.go","round":1,"startLine":3,"symbol":"B","tool":"first"},{"chars":1,"endLine":6,"path":"c.go","round":3,"startLine":5,"symbol":"","tool":"users"}],`+
		`"evicted":[{"endLine":4,"from":"first","path":"d.go","round":3,"startLine":1,"symbol":"D","tool":"users"}],`+
		`"fts":0,"graph":0,"masked":2,"named":0,`+
		`"refused":[{"endLine":9,"path":"e.go","round":2,"startLine":7,"symbol":"","tool":"read_file"}],`+
		`"rounds":3,"sources":[{"endLine":2,"path":"a.go","round":2,"startLine":1,"symbol":"","tool":"read_file"}],"stop":"rounds","symbol":0,"vector":0,`+
		`"window":{"background":5,"context":524288,"evidence":80,"history":10,"threshold":419430}}`, len(sources))
	if len(used) != 1 || string(b) != want {
		t.Errorf("%s\nwant %s", b, want)
	}
	all := `"sources":[{"endLine":2,"path":"a.go","round":2,"startLine":1,"symbol":"","tool":"read_file"},` +
		`{"endLine":3,"path":"b.go","round":1,"startLine":3,"symbol":"B","tool":"first"},{"endLine":6,"path":"c.go","round":3,"startLine":5,"symbol":"","tool":"users"}]`
	got.Evicted, got.Refused = nil, nil
	if b, _ := json.Marshal(evidenceEvent(got, got.Hits, 9, evidence.Limit{Chars: math.MaxInt}, parts, fit)); !strings.Contains(string(b), `"dropped":[]`) || !strings.Contains(string(b), all) ||
		!strings.Contains(string(b), `"evicted":[]`) || !strings.Contains(string(b), `"refused":[]`) || !strings.Contains(string(b), `"chars":{"rendered":9}`) {
		t.Errorf("nothing left out: %s", b)
	}
}

// TestCodemap: a code map is drawn with drawing events, its locations linked
// (a path's brackets escaped) and cited, and stored to be read back; an answer
// that is no map is told as errSchema, and logged; a map that shows no line of
// the sources, twice, as errMap. A context that holds a plain answer's request
// but not a code map's with its retry tells the asker the context it needs.
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
	m := codemap.Request{Repo: "o/r", Commit: "abc", Question: "How is a Page rendered?", Language: "English"} // as Ask asks
	if err := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m", MaxTokens: 100, Window: (codemap.Fixed(m, &ai.OpenAI{}, harness.New(&ai.OpenAI{}, tokens).Prompt) + 2000) * 10 / 8}).Ask(ctx, Request{RepoID: 1, Question: m.Question, Codemap: true, UserID: 1}, func(string, any) {}); !errors.As(err, new(*harness.ErrSmall)) {
		t.Fatalf("a context without the retry's room: %v", err)
	}
}
