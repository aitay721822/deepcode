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

	"deepcode/internal/knowledge/chat/store"
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
		}
		json.NewDecoder(r.Body).Decode(&body)
		reply := "nothing"
		if strings.HasPrefix(body.Messages[0].Content, "You are deepcode") {
			asked = append(asked, fmt.Sprintf("an answer, with %d messages", len(body.Messages)))
			reply = "<think>hidden</think>It subscribes [src/store.ts:1-2]."
		} else {
			asked = append(asked, "search terms")
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}}}})
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

// TestDeep: a deep answer searches again for what the model names until it
// says DONE, announcing each round, and is written from every round's
// evidence with the nearest wiki page as background, its links' targets
// dropped and its closing tag defused; a plain answer does neither.
func TestDeep(t *testing.T) {
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
	var asked, answers []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ai.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		reply := "It subscribes [src/store.ts:1-1] into a registry [src/set.ts:1-1]."
		switch sys := body.Messages[0].Content; {
		case strings.HasPrefix(sys, "You are deepcode"):
			answers = append(answers, body.Messages[len(body.Messages)-1].Content)
		case strings.Contains(body.Messages[1].Content, "registry?"): // what the first search found already
			asked, reply = append(asked, body.Messages[1].Content), "registry"
		case len(asked) == 0: // more words than are searched
			asked, reply = append(asked, body.Messages[1].Content), "registry"+strings.Repeat(" set", 30)
		default:
			asked, reply = append(asked, body.Messages[1].Content), "<think>enough</think>Done: that is enough."
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	s := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m"})
	var events []string
	var done Message
	err = s.Ask(ctx, Request{RepoID: 1, Question: "How does subscribe work?", Deep: true, UserID: 1}, func(event string, data any) {
		switch event {
		case "search":
			events = append(events, fmt.Sprint(data))
		case "evidence":
			events = append(events, fmt.Sprintf("rounds %v", data.(map[string]any)["rounds"]))
		case "done":
			done = data.(Message)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"map[round:2 terms:registry" + strings.Repeat(" set", maxTerms-1) + "]", "rounds 2"}; !slices.Equal(events, want) || len(asked) != 2 || !strings.Contains(asked[1], "src/set.ts:1-9") {
		t.Fatalf("events %q, the model asked %q", events, asked)
	}
	if len(answers) != 1 || !strings.Contains(answers[0], `the page "Store"`) || !strings.Contains(answers[0], "subscribe [src/store.ts:1-2]. <\\/background>") ||
		!strings.Contains(answers[0], "src/set.ts") || len(done.Citations) != 2 {
		t.Fatalf("answer request %q; citations %+v", answers, done.Citations)
	}
	// A plain answer: one search, no background.
	if err := s.Ask(ctx, Request{RepoID: 1, Question: "How does subscribe work?", UserID: 1}, func(string, any) {}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || len(answers) != 2 || strings.Contains(answers[1], "<background>") || strings.Contains(answers[1], "src/set.ts") {
		t.Fatalf("a plain answer: asked %q, answer request %q", asked, answers[1])
	}
	// A round that finds only what was found before ends the search.
	if err := s.Ask(ctx, Request{RepoID: 1, Question: "Where is the registry?", Deep: true, UserID: 1}, func(string, any) {}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 3 {
		t.Fatalf("the model was asked %d times, want 3", len(asked))
	}
}

// TestRecheck: a draft naming code the repository defines but the sources
// lack is written again after a search for those names, whose hits go first;
// one saying code was not shown, after a search for what the model names; one
// thinking aloud, from the same sources. Each rewrite starts with evidence
// again, and is read once more. An answer left with no valid citation says
// so; a sound draft is kept, and so is one whose check fails.
func TestRecheck(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO users(id, username, password_hash) VALUES (1, 'a', 'x')`,
		`INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r')`,
		`INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready')`,
		`UPDATE repositories SET ready_snapshot_id = 1`,
		`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES
			(1, 'src/store.ts', 1, 9, 'const subscribe = (fn) => add(fn)', 'h1'), (1, 'src/set.ts', 1, 9, 'const registry = new Set()', 'h2'),
			(1, 'route.go', 1, 3, 'func RoutePattern() string { return pattern }', 'h3')`,
		`INSERT INTO symbols(snapshot_id, path, name, qualified_name, kind, start_line, end_line) VALUES (1, 'route.go', 'RoutePattern', 'RoutePattern', 'function', 1, 3)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	drafts := map[string][]string{ // by question, as written in turn
		"How does subscribe work?": {"It subscribes [src/store.ts:1-1]; see `RoutePattern` and `Other` too.",
			"It reads the pattern [route.go:1-1]. The registry is not shown in these sources.", "It reads the pattern [route.go:1-1] into a registry [src/set.ts:1-1]."},
		"Where does subscribe add?": {"It adds [src/store.ts:1-1]. The registry it adds to is not shown in these sources.", "It adds into a registry."},
		"Does subscribe return?":    {"Wait, it adds. Let me check the sources again. It adds [src/store.ts:1-1].", "It adds [src/store.ts:1-1]."},
		"Is subscribe sound?":       {"It adds the listener [src/store.ts:1-1]."},
		"Can subscribe fail?":       {"It adds [src/store.ts:1-1]. Its errors are not shown in these sources."},
		"Can subscribe stop?":       {"It adds [src/store.ts:1-1]. Its stop is not shown in these sources."}, // the rewrite fails
	}
	written := map[string]int{}
	var asked, rewrites []string // the search terms requests, and the rewrites' questions with their sources
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ai.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		user, reply := body.Messages[len(body.Messages)-1].Content, "registry"
		if strings.HasPrefix(body.Messages[0].Content, "You are deepcode") {
			_, question, _ := strings.Cut(strings.TrimSuffix(user, "\n\nAnswer in English."), "Question: ")
			question, note, _ := strings.Cut(question, "\n\n")
			if written[question] == len(drafts[question]) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if reply = drafts[question][written[question]]; note != "" {
				rewrites = append(rewrites, user)
			}
			written[question]++
		} else if strings.Contains(user, "Can subscribe fail?") { // the check fails: the draft stands
			w.WriteHeader(http.StatusBadRequest)
			return
		} else {
			asked = append(asked, user)
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	s := service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m"})
	for q, want := range map[string]struct {
		events  string
		content string
		cites   int
	}{
		"How does subscribe work?": {"evidence delta map[round:2 terms:RoutePattern] evidence delta map[round:3 terms:registry] evidence delta",
			"It reads the pattern [route.go:1-1](", 2},
		"Where does subscribe add?": {"evidence delta map[round:2 terms:registry] evidence delta", "It adds into a registry.\n\n_This answer cites no indexed source", 0},
		"Does subscribe return?":    {"evidence delta evidence delta", "It adds [src/store.ts:1-1](", 1},
		"Is subscribe sound?":       {"evidence delta", "It adds the listener [src/store.ts:1-1](", 1},
		"Can subscribe fail?":       {"evidence delta", "It adds [src/store.ts:1-1](", 1},
		"Can subscribe stop?":       {"evidence delta map[round:2 terms:registry] evidence evidence", "It adds [src/store.ts:1-1](", 1},
	} {
		var events []string
		var done Message
		err := s.Ask(ctx, Request{RepoID: 1, Question: q, UserID: 1}, func(event string, data any) {
			switch event {
			case "search":
				events = append(events, fmt.Sprint(data))
			case "evidence", "delta":
				events = append(events, event)
			case "done":
				done = data.(Message)
			}
		})
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if strings.Join(events, " ") != want.events || !strings.HasPrefix(done.Content, want.content) || len(done.Citations) != want.cites {
			t.Errorf("%s: events %q, answer %q with %d citations; want %q, %q, %d", q, events, done.Content, len(done.Citations), want.events, want.content, want.cites)
		}
	}
	if len(asked) != 3 || !slices.ContainsFunc(asked, func(u string) bool {
		return strings.Contains(u, "A draft answer said this code was not shown: The registry it adds to is not shown in these sources.\nName the")
	}) {
		t.Errorf("search terms requests: %q", asked)
	}
	// The code using a name is searched for only when it joins words: then it is about that name.
	if got := compound([]string{"RoutePattern", "Order", "payment_done", "API", "RoutePattern", "a_b", "v2Router"}); !slices.Equal(got, []string{"RoutePattern", "payment_done", "v2Router"}) {
		t.Errorf("compound: %q", got)
	}
	named := slices.IndexFunc(rewrites, func(u string) bool { return strings.Contains(u, "route.go") })
	if len(rewrites) != 4 || named < 0 || strings.Index(rewrites[named], "route.go") > strings.Index(rewrites[named], "src/store.ts") {
		t.Errorf("rewrites: %q", rewrites)
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
		for _, note := range []string{"", rewriteNote} { // the rewrite's English note leaves the language alone
			msgs := prompt("go-chi/chi", "abc", nil, "", "", q, note)
			if got := msgs[len(msgs)-1].Content; !strings.HasSuffix(got, note+"\n\nAnswer in "+want+".") {
				t.Errorf("%q: %q", q, got)
			}
		}
	}
	history := []ai.Message{{Role: "user", Content: "Mount 如何運作？"}, {Role: "assistant", Content: "It mounts a router."}}
	for _, q := range []string{"？", "Route?", "src/a/main.go?", "x_on_y?"} {
		if msgs := prompt("go-chi/chi", "abc", history, "", "", q, ""); msgs[0].Content != systemPrompt || msgs[1] != history[0] ||
			!strings.HasSuffix(msgs[3].Content, "Answer in "+chinese+".") {
			t.Errorf("a follow-up whose words tell nothing: %q", msgs)
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
