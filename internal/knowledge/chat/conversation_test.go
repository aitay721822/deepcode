package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/chat/background"
	"deepcode/internal/knowledge/chat/harness"
	"deepcode/internal/knowledge/codemap"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/compat"
	"deepcode/internal/platform/ai/tokens"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
)

// TestAskSCIPContext: the answer's request holds each users_at lookup's exact reference lines, a few per chunk,
// says so when SCIP holds none, when a lookup fell back to names and when more exist, never lists another
// symbol's line (beta.Resolve on main.go:10), and keeps each lookup's verdict when its locations are cut to fit.
func TestAskSCIPContext(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seed := `INSERT INTO users(id, username, password_hash) VALUES (1, 'a', 'x');
		INSERT INTO repositories(id, url, owner, name) VALUES (1, 'https://github.com/o/r', 'o', 'r');
		INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready');
		UPDATE repositories SET ready_snapshot_id = 1;
		INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash)
			VALUES (1, 'main.go', 9, 11, 'alpha.Resolve()' || char(10) || 'beta.Resolve()' || char(10) || 'alpha.Resolve()', 'h');
		INSERT INTO scip_occurrences(snapshot_id, path, line, byte_column, symbol, definition_name) VALUES
			(1, 'alpha.go', 3, 5, 'alpha.Resolve', 'Resolve'), (1, 'main.go', 9, 6, 'alpha.Resolve', ''), (1, 'main.go', 10, 5, 'beta.Resolve', ''),
			(1, 'main.go', 11, 6, 'alpha.Resolve', ''), (1, 'alpha.go', 4, 5, 'alpha.Unused', 'Unused'), (1, 'alpha.go', 5, 5, 'alpha.Busy', 'Busy');`
	for i := range 3 { // Busy is called on all four lines of three chunks, each in a file with a long path
		seed += strings.ReplaceAll(`INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'P', 1, 4, 'Busy()' || char(10) || 'Busy()' || char(10) || 'Busy()' || char(10) || 'Busy()', 'h');
			INSERT INTO scip_occurrences(snapshot_id, path, line, byte_column, symbol, definition_name) VALUES (1, 'P', 1, 0, 'alpha.Busy', ''), (1, 'P', 2, 0, 'alpha.Busy', ''), (1, 'P', 3, 0, 'alpha.Busy', ''), (1, 'P', 4, 0, 'alpha.Busy', '');`,
			"'P'", fmt.Sprintf("'pkg/%s/%d/main.go'", strings.Repeat("long-directory/", 15), i))
	}
	if _, err := db.Exec(seed); err != nil {
		t.Fatal(err)
	}
	exact, empty := `{"name":"Resolve","path":"alpha.go","line":3}`, `{"name":"Unused","path":"alpha.go","line":4}`
	fallback, busy := `{"name":"Resolve","path":"alpha.go","line":99}`, `{"name":"Busy","path":"alpha.go","line":5}`
	for _, tc := range []struct {
		name, question string
		args, want     []string
		context        int
	}{
		{"two exact lines in one source chunk", "where is Resolve called?", []string{exact}, []string{`"alpha.go":3`, `"main.go":9, 11`}, 0},
		{"no indexed references", "zzzz qqqq", []string{empty}, []string{`"alpha.go":4`, "No SCIP-indexed references", "may not cover"}, 0},
		{"fallback", "where is Resolve called?", []string{fallback}, []string{`"alpha.go":99`, "name-based search", "not verified"}, 0},
		{"sample spans multiple chunks", "where is Busy called?", []string{busy}, []string{`"alpha.go":5`, `/0/main.go":1, 2`, `/1/main.go":1`, `/2/main.go":1`, "More indexed references exist"}, 0},
		{"bounded context preserves verdicts", "where are Busy and Resolve called?", []string{busy, empty, exact, fallback},
			[]string{`"alpha.go":5`, `"alpha.go":4`, `"alpha.go":3`, `"alpha.go":99`, "not verified", "Some SCIP lookups or locations were omitted"}, 8192},
		{"retry with smaller lookup budget", "where is Resolve called?", []string{exact}, []string{"Some SCIP lookups or locations were omitted", "Do not treat other lines as SCIP-verified"}, 4000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			final := make(chan string, 1)
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				msgs, tools := request(r)
				if tools && slices.ContainsFunc(msgs, func(m ai.Message) bool { return m.Role == "tool" }) {
					gathered(w, "")
				} else if tools { // the first gathering reply calls users_at with each of tc.args
					var calls []string
					for i, arg := range tc.args {
						calls = append(calls, fmt.Sprintf(`{"id":"c%d","type":"function","function":{"name":"users_at","arguments":%q}}`, i, arg))
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[%s]}}]}`, strings.Join(calls, ","))
				} else {
					if strings.HasPrefix(msgs[0].Content, "You are deepcode") {
						final <- msgs[len(msgs)-1].Content
					}
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer model.Close()
			s := service(db, &compat.Client{BaseURL: model.URL, ModelName: "m", Context: tc.context, MaxTokens: 512})
			if err := s.Ask(t.Context(), Request{RepoID: 1, Question: tc.question, UserID: 1}, func(string, any) {}); err != nil || len(final) == 0 {
				t.Fatal("no answer request:", err)
			}
			prompt := <-final
			_, notes, ok := strings.Cut(prompt, "SCIP reference lookups:\n")
			if !ok {
				_, notes, _ = strings.Cut(prompt, "\n\n")
			}
			if notes, _, _ = strings.Cut(notes, "\n\n"); regexp.MustCompile(`"main\.go":[\d, ]*\b10\b`).MatchString(notes) { // wherever 10 falls in main.go's lines
				t.Errorf("unrelated reference in SCIP context: %q", notes)
			}
			for _, want := range tc.want {
				if !strings.Contains(notes, want) {
					t.Errorf("SCIP context missing %q: %q", want, notes)
				}
			}
		})
	}
}

// TestOpenOwnConversation: a conversation is continued only by its owner and only in its repository;
// to anyone else it does not exist. Its history, and the summary of what does not fit the model's context.
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
		`INSERT INTO conversations(id, public_id, repository_id, snapshot_id, title, user_id) VALUES (1, 'c1', 1, 1, 't', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s := service(db, nil)
	at := catalog.Ref{ID: 1}
	if b, err := s.open(ctx, Request{UserID: 1, RepoID: 1, ConversationID: "c1"}, at, "q"); err != nil || b.id != 1 || b.public != "c1" {
		t.Fatalf("the owner: %+v %v", b, err)
	}
	for _, req := range []Request{{UserID: 2, RepoID: 1, ConversationID: "c1"}, {UserID: 1, RepoID: 2, ConversationID: "c1"}, {UserID: 1, RepoID: 1, ConversationID: "1"}} {
		if _, err := s.open(ctx, req, at, "q"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%+v: %v", req, err)
		}
	}
	// The history keeps its first message while it grows to 11, then drops 6 at once.
	for _, n := range []int{0, 5, 11, 12, 17} { // in order: the summary below is of the last's
		first := map[int]string{5: "m1", 11: "m1", 12: "m7", 17: "m7"}[n]
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
	// No context known: all whole, no summary. Past its share (20 tokens, none for the page) it is summed up, stored, told and leads the new message (no turn kept), then reused; an older one stored after leaves it, a newer replaces it.
	calls, told := 0, []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"{\"topics\":[\"m\"],\"names\":[],\"open\":[]}"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	turns, _ := s.history(ctx, 1)
	a := asking{conv: 1, fit: harness.New(&compat.Client{BaseURL: srv.URL, ModelName: "m", Context: 524288}, o200k), fixed: 419430 - 2520, turns: turns, page: background.Of("Store", strings.Repeat("a", 900)), emit: func(event string, data any) { told = append(told, fmt.Sprint(event, " ", data)) }}
	if parts, err := s.fit(ctx, asking{conv: 1, fit: harness.New(&compat.Client{}, o200k), fixed: 1 << 20, turns: turns, page: a.page, emit: a.emit}, evidenceChars); err != nil || calls+len(told) != 0 ||
		len(parts.history) != len(turns) || parts.background != a.page.Block(1<<62, o200k.Count) || parts.shares != (harness.Shares{Evidence: evidenceChars, History: a.fit.History(turns), Background: o200k.Count(parts.background)}) {
		t.Fatalf("no context known: %v %+v", err, parts)
	}
	for range 2 {
		parts, err := s.fit(ctx, a, evidenceChars)
		through, text, _ := s.Store.Summary(ctx, 1)
		if err != nil || calls != 1 || parts.shares.Evidence != 2500 || len(parts.history) != 0 || !strings.HasPrefix(parts.background, "Earlier in") || o200k.Count(parts.background) > 20 ||
			through != turns[len(turns)-1].ID || text != "Named: none. Topics: m. Open: none." || !slices.Equal(told, []string{"compact map[summarized:11]"}) {
			t.Fatalf("%v: %d calls, %+v, stored %d %q, told %q", err, calls, parts, through, text, told)
		}
	}
	if err, kept := s.Store.SetSummary(ctx, 1, 3, "older"), fmt.Sprintln(s.Store.Summary(ctx, 1)); err != nil || kept != fmt.Sprintln(turns[len(turns)-1].ID, "Named: none. Topics: m. Open: none.", nil) || s.Store.SetSummary(ctx, 1, turns[len(turns)-1].ID+1, "newer") != nil || fmt.Sprintln(s.Store.Summary(ctx, 1)) != fmt.Sprintln(turns[len(turns)-1].ID+1, "newer", nil) {
		t.Errorf("stored again: %v %s %s", err, kept, fmt.Sprintln(s.Store.Summary(ctx, 1)))
	}
}

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

// TestCodemap: a code map is drawn with drawing events, its locations linked
// (a path's brackets escaped) and cited, and stored to be read back; an answer
// that is no map is told as errSchema, and logged; a map that shows no line of
// the sources, twice, as errMap; a question no code matches, as an answer's in
// its language. A context that holds a plain answer's request
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
		if _, tools := request(r); tools {
			gathered(w, "")
			return
		}
		reply := drawn
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	s := service(db, &compat.Client{BaseURL: model.URL, ModelName: "m"})
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
	kept, drawn := drawn, "zzzz" // the language and the search terms named: none matches a chunk, so no map is drawn
	for q, want := range map[string]string{"zzzz qqqq": "No indexed code matched this question, so it can't be confirmed from the current code.", "zzzz 什麼": "無法從目前程式碼確認：沒有任何已索引的程式碼符合這個問題。"} {
		err := s.Ask(ctx, Request{RepoID: 1, Question: q, Codemap: true, UserID: 1}, func(event string, data any) {
			if event == "done" {
				done = data.(Message)
			}
		})
		if err != nil || done.Content != want || len(done.Citations) != 0 {
			t.Fatalf("no evidence for %q: %v %+v", q, err, done)
		}
	}
	drawn = kept
	m := codemap.Request{Repo: "o/r", Commit: "abc", Question: "How is a Page rendered?", Language: "English"} // as Ask asks
	if err := service(db, &compat.Client{BaseURL: model.URL, ModelName: "m", MaxTokens: 100, Context: (codemap.Fixed(m, &compat.Client{}, harness.New(&compat.Client{}, o200k).Prompt) + 2000) * 10 / 8}).Ask(ctx, Request{RepoID: 1, Question: m.Question, Codemap: true, UserID: 1}, func(string, any) {}); !errors.As(err, new(*harness.ErrSmall)) {
		t.Fatalf("a context without the retry's room: %v", err)
	}
}

// TestRefitted: an answer's request refused as too long is fitted again from
// the counts its parts were fitted with, the context learned, and sent once
// more. Its own lines, the conversation's messages, the background and each
// evidence block are counted alone: no text counted is the request's whole
// message. A follow-up at a middling context keeps its conversation whole:
// the evidence is fitted at the tokens it was gathered at, not at the share it
// was gathered within, which would leave the conversation summaryRoom and have
// the model sum it up.
func TestRefitted(t *testing.T) {
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
			(1, 'a.ts', 1, 1, 'const subscribe = "' || hex(zeroblob(1500)) || '"', 'h1'), (1, 'b.ts', 1, 1, 'const subscribe = "' || hex(zeroblob(1500)) || '"', 'h2'),
			(1, 'c.ts', 1, 1, 'const subscribe = "' || hex(zeroblob(1500)) || '"', 'h3')`, // 3,000 bytes each: together past the system prompt
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var asked []string // each request's new message but the gathering's, the refused one first
	var messages []int // and its messages
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msgs, tools := request(r)
		if tools {
			gathered(w, "")
			return
		}
		reply := "It subscribes [a.ts:1-1]."
		if msgs[0].Content == languagePrompt {
			reply = "English"
		} else if asked, messages = append(asked, msgs[len(msgs)-1].Content), append(messages, len(msgs)); len(asked) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":400,"message":"too long","type":"exceed_context_size_error","n_prompt_tokens":419430,"n_ctx":524288}}`) // the threshold: room for exactly what was sent
			return
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	longest, again, times, s := 0, 0, map[string]int{}, service(db, &compat.Client{BaseURL: model.URL, ModelName: "m"})
	s.Tokens = tokens.Counting("o200k, the longest text kept", func(text string) int {
		longest, again, times[text] = max(longest, len(text)), again+times[text], times[text]+1
		return o200k.Count(text)
	})
	var done Message
	err = s.Ask(ctx, Request{RepoID: 1, Question: "subscribe", UserID: 1}, func(event string, data any) {
		if event == "done" {
			done = data.(Message)
		}
	})
	if err != nil || len(asked) != 2 || asked[1] != asked[0] || strings.Count(asked[0], "<source path=") != 3 || len(done.Citations) != 1 || s.Chat.Window().Context != 524288 {
		t.Fatalf("%v: %d answer requests, the context %d, %+v", err, len(asked), s.Chat.Window().Context, done)
	}
	if len(asked[0]) <= len(systemPrompt) || longest >= len(strings.TrimSpace(asked[0])) || again != 0 {
		t.Errorf("%d bytes counted at once, %d texts counted again; the request's message is %d, the system prompt %d", longest, again, len(asked[0]), len(systemPrompt))
	}
	for range 2 { // with the first answer, 6 turns of some 1,300 tokens, past summaryRoom
		s.Store.AddMessage(ctx, 1, "user", "subscribe", nil)
		s.Store.AddMessage(ctx, 1, "assistant", strings.Repeat("word ", 600), nil)
	}
	var conv string // the first answer's conversation
	db.QueryRowContext(ctx, `SELECT public_id FROM conversations WHERE id = 1`).Scan(&conv)
	s = service(db, &compat.Client{BaseURL: model.URL, ModelName: "m", Context: 25000, MaxTokens: 100}) // a threshold of 20,000
	if err := s.Ask(ctx, Request{RepoID: 1, ConversationID: conv, Question: "subscribe", UserID: 1}, func(string, any) {}); err != nil || len(asked) != 3 || messages[2] != 8 {
		t.Errorf("a follow-up: %v, %d requests, %v messages", err, len(asked), messages)
	}
}

// TestLanguage: code reads a language only from the script, quotes and code
// aside: kana make it Japanese and Han Chinese, however much English code the
// question names; two words or more of another script are a sentence for the
// model to name; fewer tell nothing, and the conversation's questions before
// it, newest first, go on, a question of one word left for the model at the
// end. The model's reply is a name or none.
func TestLanguage(t *testing.T) {
	for q, want := range map[string]string{"Mount 如何運作？": chinese, "Mount はどう動きますか": "Japanese", "What is 路由?": chinese, // unquoted: the known limit
		"為何 PaymentSucceededEventHandler 會觸發 InvoiceProjection": chinese, "src/app/server.go 的 Route 在哪裡": chinese,
		"請解釋 the router matching algorithm in detail please": chinese, "這段為何失敗：```go\nfunc (r *Router) Mount() {}\n```": chinese} {
		if lang, words := language(q); lang != want || words != 0 {
			t.Errorf("%q: %q %d, want %q", q, lang, words, want)
		}
	}
	for _, q := range []string{"How does Mount work?", "Wie funktionieren Middlewares in chi, und wie werden sie mit Use, With und Group verkettet?", // no word list
		"Как работает Mount?", "마운트는 어떻게 동작하나요", "Is the café example in the docs current?", "Mount fails.", "Isn't Route mounted?", "Welche Middlewares?",
		"What does the 「錯誤」 message in handler.go mean?", "What does `無法從目前程式碼確認` mean?", "What does \"錯誤\" mean?", "What does “錯誤” mean?",
		"What does ‘錯誤’ mean?", "Why does this fail? ```go\n// 錯誤"} { // a fence left open runs to the end
		if lang, words := language(q); lang != "" || words < 2 {
			t.Errorf("%q: %q %d, want a sentence", q, lang, words)
		}
	}
	for q, want := range map[string]int{"？": 0, "Route?": 1, "How?": 1, "Does chi.Mux.Use?": 1, "src/a/main.go?": 0, "x_on_y?": 0, "src/a/main.go:12 x_on_y?": 0,
		"my-handler-func?": 0, "v2 http2?": 0, "---": 0, "- ？": 0, "src/a.go ...": 0, "... ---": 0} {
		if lang, words := language(q); lang != "" || words != want {
			t.Errorf("%q: %q %d, want %d words", q, lang, words, want)
		}
	}
	zh := []ai.Message{{Role: "user", Content: "Mount 如何運作？"}, {Role: "assistant", Content: "It mounts a router."}}
	for _, c := range []struct {
		q         string
		history   []ai.Message
		lang, ask string
	}{{"？", nil, own, ""}, {"x_on_y?", nil, own, ""}, {"Why?", nil, "", "Why?"}, {"Why?", []ai.Message{{Role: "user", Content: "Route?"}}, "", "Why?"}, // one word, nothing before tells
		{"Route?", zh, chinese, ""}, {"Do we validate tokens?", zh, "", "Do we validate tokens?"}, {"Welche Middlewares?", zh, "", "Welche Middlewares?"},
		{"x_on_y?", append(slices.Clone(zh), ai.Message{Role: "user", Content: "Route?"}), chinese, ""},                                  // past the questions that tell nothing
		{"x_on_y?", append(slices.Clone(zh), ai.Message{Role: "user", Content: "Do we validate tokens?"}), "", "Do we validate tokens?"}, // the newest that tells
		{"Route?", []ai.Message{{Role: "user", Content: "Как работает Mount?"}}, "", "Как работает Mount?"}, {"x_on_y?", []ai.Message{{Role: "user", Content: "Mount はどう動きますか"}}, "Japanese", ""}} {
		if lang, ask := languageOf(c.q, c.history); lang != c.lang || ask != c.ask {
			t.Errorf("%q after %d messages: %q %q, want %q %q", c.q, len(c.history), lang, ask, c.lang, c.ask)
		}
	}
	for reply, want := range map[string]string{"German": "German", " German.\n": "German", "Brazilian Portuguese": "Brazilian Portuguese", "Chinese": chinese, "chinese": chinese,
		"": "", "Written in plain German": "", "German (Deutsch)": "", "It subscribes [a.ts:1-1].": "", strings.Repeat("a", 31): ""} {
		if got := named(reply); got != want {
			t.Errorf("named(%q) = %q, want %q", reply, got, want)
		}
	}
	if uncited(chinese) == uncited("German") {
		t.Error("the footer")
	}
}

// TestRules: the request ends with namesLine, the line saying the user reads
// no block, a Chinese answer's words for what can't be confirmed (no other
// answer's, not one the model named "Traditional Chinese") and the language
// line, last. The rules hold no Chinese and quote no such sentence to copy,
// and keep saying the blocks are part of the repository, which the user never
// sees, and that the language's own behaviour needs no citation; past the rule
// naming the words an answer must not speak of the code in, neither the rules
// nor the request name it in them (materialRe).
func TestRules(t *testing.T) {
	const reader = " Write about the repository, never about the <source> blocks."
	for lang, end := range map[string]string{chinese: reader + " Write what can't be confirmed as 「無法從目前程式碼確認」. Answer in Traditional Chinese, as used in Taiwan.",
		"German": reader + " Answer in German.", "English": reader + " Answer in English.", "Traditional Chinese": reader + " Answer in Traditional Chinese."} {
		if d := prompt("go-chi/chi", "abc", nil, "", "", "Wie?", lang).Dynamic; !strings.HasSuffix(d, "Question: Wie?\n\n"+namesLine+end) {
			t.Errorf("%s: %q", lang, d)
		}
	}
	if strings.ContainsFunc(systemPrompt, isHan) || slices.ContainsFunc(quotedRe.FindAllString(systemPrompt, -1), func(q string) bool { return strings.Contains(q, "confirm") }) {
		t.Error("the rules hold Han or quote what can't be confirmed")
	}
	readerRule := "- The user reads your answer, never these rules or the <source> blocks, so write about the repository itself, never about the <source> blocks: in no language say what code you were or were not shown, provided or given, what the message does or does not include, or what code you could see.\n"
	for _, rule := range []string{"which hold only part of the repository: what they do not hold may still exist in it", readerRule,
		"never say the repository lacks something only because the blocks do not hold it",
		"Behaviour of the language or its standard library that the cited code relies on may be stated as theirs, naming them (net/http, the Go runtime), without a citation; anything else outside the repository can't be confirmed from the current code."} {
		if !strings.Contains(systemPrompt, rule) {
			t.Errorf("the rules lack %q", rule)
		}
	}
	rest := strings.Replace(systemPrompt, readerRule, "", 1) + prompt("go-chi/chi", "abc", nil, "", "", "Wie?", chinese).Dynamic
	if m := materialRe.FindString(blocksRe.ReplaceAllString(rest, "")); m != "" {
		t.Errorf("the rules or the request say %q", m)
	}
}

// materialRe holds the words answers spoke of the code they were given in
// ("not shown", "the provided sources", "isn't included"); blocksRe is a
// <source> tag, one the rules name or their example's, which has no end tag,
// so the request TestRules reads carries no blocks to strip.
var (
	materialRe = regexp.MustCompile(`(?i)\b(show\w*|cover\w*|provid\w*|given|excerpt\w*|sources?|visible|available|snippet\w*|includ\w*)\b`)
	blocksRe   = regexp.MustCompile(`</?source\b[^>]*>`)
)

// TestAnswerLanguage: a language the script tells costs no request; a
// sentence is named by the model in one, the question alone after the
// language prompt, and a follow-up that tells nothing has the question before
// it named, or its own word with nothing before; a reply that names none, or a
// failed request, leaves the answer in the question's own.
func TestAnswerLanguage(t *testing.T) {
	var asked []ai.Message
	reply, status := "German", http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msgs, _ := request(r)
		asked = append(asked, msgs...)
		if w.WriteHeader(status); status == http.StatusOK {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		}
	}))
	defer srv.Close()
	ctx, s := context.Background(), &Service{Chat: &compat.Client{BaseURL: srv.URL, ModelName: "m"}}
	if got := s.answerLanguage(ctx, "Mount 如何運作？", nil); got != chinese || len(asked) != 0 {
		t.Errorf("Han: %q after %d messages sent", got, len(asked))
	}
	q := "Wie funktionieren Middlewares in chi?"
	if got := s.answerLanguage(ctx, q, nil); got != "German" || len(asked) != 2 || asked[0].Content != languagePrompt || asked[1] != (ai.Message{Role: "user", Content: q}) {
		t.Errorf("a sentence: %q, sent %+v", got, asked)
	}
	asked, reply = nil, "French"
	if got := s.answerLanguage(ctx, "Route?", []ai.Message{{Role: "user", Content: "Est-ce que chi a un middleware pour CORS ?"}, {Role: "assistant", Content: "Oui."}}); got != "French" ||
		len(asked) != 2 || asked[1].Content != "Est-ce que chi a un middleware pour CORS ?" {
		t.Errorf("a follow-up: %q, sent %+v", got, asked)
	}
	asked, reply = nil, "English"
	if got := s.answerLanguage(ctx, "Why?", nil); got != "English" || len(asked) != 2 || asked[1].Content != "Why?" {
		t.Errorf("one word: %q, sent %+v", got, asked)
	}
	reply = "It subscribes [a.ts:1-1]."
	if got := s.answerLanguage(ctx, q, nil); got != own {
		t.Errorf("no name: %q", got)
	}
	status = http.StatusInternalServerError
	if got := s.answerLanguage(ctx, q, nil); got != own {
		t.Errorf("a failed request: %q", got)
	}
}
