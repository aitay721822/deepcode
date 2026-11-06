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

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
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
	s := &Service{DB: db, Catalog: &catalog.Catalog{DB: db, Queue: &jobs.Queue{DB: db}}, Retriever: &retrieval.Retriever{DB: db}, Chat: &ai.OpenAI{BaseURL: model.URL, ModelName: "m"}}
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
	s := &Service{DB: db, Catalog: &catalog.Catalog{DB: db}}
	at := catalog.Ref{ID: 1}
	if b, err := s.open(ctx, Request{UserID: 1, RepoID: 1, ConversationID: 1}, at, "q"); err != nil || b.id != 1 {
		t.Fatalf("the owner: %+v %v", b, err)
	}
	for _, req := range []Request{{UserID: 2, RepoID: 1, ConversationID: 1}, {UserID: 1, RepoID: 2, ConversationID: 1}} {
		if _, err := s.open(ctx, req, at, "q"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%+v: %v", req, err)
		}
	}
}
