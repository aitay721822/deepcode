package resume

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graphstore"
	"deepcode/internal/repo/index/indexer/vectorstore"
)

// TestModelErrors: what a model server does to a request says whether a later
// try may get through. A status the SDK retries is unavailable once those
// retries are spent, unless the account is out of credit, which is refused,
// or the request was longer than the context, which is neither; a refused
// key or an unknown model is refused at once; a bad request is neither; a
// server that cannot be reached and an answer cut midway are unavailable.
func TestModelErrors(t *testing.T) {
	quota := `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`
	overflow := `{"error":{"code":"context_length_exceeded","message":"too long"}}`
	for _, c := range []struct {
		status   int
		body     string
		want     error // ai.ErrUnavailable, ai.ErrRefused or nil
		requests int
	}{
		{http.StatusServiceUnavailable, "", ai.ErrUnavailable, 4},
		{http.StatusTooManyRequests, "", ai.ErrUnavailable, 4},
		{http.StatusTooManyRequests, quota, ai.ErrRefused, 4},
		{http.StatusServiceUnavailable, overflow, nil, 4},
		{http.StatusUnauthorized, "", ai.ErrRefused, 1},
		{http.StatusForbidden, "", ai.ErrRefused, 1},
		{http.StatusNotFound, "", ai.ErrRefused, 1},
		{http.StatusBadRequest, "", nil, 1},
	} {
		m := newModel()
		m.fail(c.status, c.body)
		srv := httptest.NewServer(m)
		_, err := (&ai.OpenAI{BaseURL: srv.URL + "/v1", ModelName: "fake-embed"}).Embed(context.Background(), []string{"x"})
		srv.Close()
		_, requests, _ := m.counts()
		if err == nil || classOf(err) != c.want || requests != c.requests || c.body == overflow && !errors.As(err, new(*ai.ErrContext)) {
			t.Errorf("status %d %s: %v after %d requests, want %v after %d", c.status, c.body, err, requests, c.want, c.requests)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + l.Addr().String() + "/v1"
	l.Close()
	if _, err := (&ai.OpenAI{BaseURL: closed, ModelName: "fake-embed"}).Embed(context.Background(), []string{"x"}); !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("a closed port: %v, want ErrUnavailable", err)
	}

	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"half an ans"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer cut.Close()
	chat := &ai.OpenAI{BaseURL: cut.URL + "/v1", ModelName: "fake-chat", Timeout: time.Minute}
	if out, err := chat.Generate(context.Background(), ai.Prompt{Dynamic: "hi"}); !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("an answer cut midway: %q, %v, want ErrUnavailable", out, err)
	}
}

// classOf is ai.ErrRefused or ai.ErrUnavailable when err is one, else nil.
func classOf(err error) error {
	for _, kind := range []error{ai.ErrRefused, ai.ErrUnavailable} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	return nil
}

// TestPageWaitsForEmbeddings: with the embedding server down, a wiki page's
// search fails, to be written once it is back, where a question's search
// goes on without vectors.
func TestPageWaitsForEmbeddings(t *testing.T) {
	m := newModel()
	m.fail(http.StatusServiceUnavailable, "")
	srv := httptest.NewServer(m)
	defer srv.Close()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}},
		Embed: &ai.OpenAI{BaseURL: srv.URL + "/v1", ModelName: "fake-embed"}, Vectors: &vectorstore.SQLite{DB: db}, Cache: cache.New(1 << 20)}
	if _, err := r.SearchPage(context.Background(), 1, "alpha beta", nil, nil, 5); !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("a page's search: %v, want ErrUnavailable", err)
	}
	if _, err := r.Search(context.Background(), 1, "alpha gamma", 5); err != nil {
		t.Errorf("a question's search: %v, want it to go on without vectors", err)
	}
}
