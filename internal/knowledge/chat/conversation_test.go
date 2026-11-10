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
		if asked, messages = append(asked, body.Messages[len(body.Messages)-1].Content), append(messages, len(body.Messages)); len(asked) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":400,"message":"too long","type":"exceed_context_size_error","n_prompt_tokens":419430,"n_ctx":524288}}`) // the threshold: room for exactly what was sent
			return
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "It subscribes [a.ts:1-1]."}, "finish_reason": "stop"}}})
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
