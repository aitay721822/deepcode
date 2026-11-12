package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/chat/background"
	"deepcode/internal/knowledge/chat/harness"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
)

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
	a := asking{conv: 1, fit: harness.New(&ai.OpenAI{BaseURL: srv.URL, ModelName: "m", Window: 524288}, tokens), fixed: 419430 - 2520, turns: turns, page: background.Of("Store", strings.Repeat("a", 900)), emit: func(event string, data any) { told = append(told, fmt.Sprint(event, " ", data)) }}
	if parts, err := s.fit(ctx, asking{conv: 1, fit: harness.New(&ai.OpenAI{}, tokens), fixed: 1 << 20, turns: turns, page: a.page, emit: a.emit}, evidenceChars); err != nil || calls+len(told) != 0 ||
		len(parts.history) != len(turns) || parts.background != a.page.Block(1<<62, tokens.Count) || parts.shares != (harness.Shares{Evidence: evidenceChars, History: a.fit.History(turns), Background: tokens.Count(parts.background)}) {
		t.Fatalf("no context known: %v %+v", err, parts)
	}
	for range 2 {
		parts, err := s.fit(ctx, a, evidenceChars)
		through, text, _ := s.Store.Summary(ctx, 1)
		if err != nil || calls != 1 || parts.shares.Evidence != 2500 || len(parts.history) != 0 || !strings.HasPrefix(parts.background, "Earlier in") || tokens.Count(parts.background) > 20 ||
			through != turns[len(turns)-1].ID || text != "Named: none. Topics: m. Open: none." || !slices.Equal(told, []string{"compact map[summarized:11]"}) {
			t.Fatalf("%v: %d calls, %+v, stored %d %q, told %q", err, calls, parts, through, text, told)
		}
	}
	if err, kept := s.Store.SetSummary(ctx, 1, 3, "older"), fmt.Sprintln(s.Store.Summary(ctx, 1)); err != nil || kept != fmt.Sprintln(turns[len(turns)-1].ID, "Named: none. Topics: m. Open: none.", nil) || s.Store.SetSummary(ctx, 1, turns[len(turns)-1].ID+1, "newer") != nil || fmt.Sprintln(s.Store.Summary(ctx, 1)) != fmt.Sprintln(turns[len(turns)-1].ID+1, "newer", nil) {
		t.Errorf("stored again: %v %s %s", err, kept, fmt.Sprintln(s.Store.Summary(ctx, 1)))
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
		var body struct {
			Messages []ai.Message `json:"messages"`
			Tools    []any        `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) > 0 {
			gathered(w, "")
			return
		}
		reply := "It subscribes [a.ts:1-1]."
		if body.Messages[0].Content == languagePrompt {
			reply = "English"
		} else if asked, messages = append(asked, body.Messages[len(body.Messages)-1].Content), append(messages, len(body.Messages)); len(asked) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":400,"message":"too long","type":"exceed_context_size_error","n_prompt_tokens":419430,"n_ctx":524288}}`) // the threshold: room for exactly what was sent
			return
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	longest, again, times, s := 0, 0, map[string]int{}, service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m"})
	s.Tokens = harness.Counting("o200k, the longest text kept", func(text string) int {
		longest, again, times[text] = max(longest, len(text)), again+times[text], times[text]+1
		return tokens.Count(text)
	})
	var done Message
	err = s.Ask(ctx, Request{RepoID: 1, Question: "subscribe", UserID: 1}, func(event string, data any) {
		if event == "done" {
			done = data.(Message)
		}
	})
	if err != nil || len(asked) != 2 || asked[1] != asked[0] || strings.Count(asked[0], "<source") != 3 || len(done.Citations) != 1 || s.Chat.Context() != 524288 {
		t.Fatalf("%v: %d answer requests, the context %d, %+v", err, len(asked), s.Chat.Context(), done)
	}
	if len(asked[0]) <= len(systemPrompt) || longest >= len(strings.TrimSpace(asked[0])) || again != 0 {
		t.Errorf("%d bytes counted at once, %d texts counted again; the request's message is %d, the system prompt %d", longest, again, len(asked[0]), len(systemPrompt))
	}
	for range 2 { // with the first answer, 6 turns of some 1,300 tokens, past summaryRoom
		s.Store.AddMessage(ctx, 1, "user", "subscribe", nil)
		s.Store.AddMessage(ctx, 1, "assistant", strings.Repeat("word ", 600), nil)
	}
	s = service(db, &ai.OpenAI{BaseURL: model.URL, ModelName: "m", Window: 25000, MaxTokens: 100}) // a threshold of 20,000
	if err := s.Ask(ctx, Request{RepoID: 1, ConversationID: 1, Question: "subscribe", UserID: 1}, func(string, any) {}); err != nil || len(asked) != 3 || messages[2] != 8 {
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
	if !strings.HasSuffix(prompt("go-chi/chi", "abc", nil, "", "", "Wie?", "German").Dynamic, "Question: Wie?\n\n"+namesLine+" Answer in German.") || uncited(chinese) == uncited("German") {
		t.Error("the language line or the footer")
	}
}

// TestAnswerLanguage: a language the script tells costs no request; a
// sentence is named by the model in one, the question alone after the
// language prompt, and a follow-up that tells nothing has the question before
// it named, or its own word with nothing before; a reply that names none, or a
// failed request, leaves the answer in the question's own.
func TestAnswerLanguage(t *testing.T) {
	var asked []ai.Message
	reply, status := "German", http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ai.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		asked = append(asked, body.Messages...)
		if w.WriteHeader(status); status == http.StatusOK {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		}
	}))
	defer srv.Close()
	ctx, s := context.Background(), &Service{Chat: &ai.OpenAI{BaseURL: srv.URL, ModelName: "m"}}
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
