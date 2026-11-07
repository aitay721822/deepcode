package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A server that repeats the key it refused gets none of it into the error:
// not whole, not where the 2048-byte cut runs through it, not past the cut.
func TestRefusedKeyMasked(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	// The key at the start, and a second copy at offset second: before, across and past the cut.
	for _, second := range []int{100, 2048 - len(key)/2, 2050} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(key + strings.Repeat("x", second-len(key)) + key + " is not a valid key"))
		}))
		c := &OpenAI{BaseURL: srv.URL, APIKey: key, ModelName: "m"}
		_, err := c.post(context.Background(), "/chat/completions", struct{}{})
		srv.Close()
		if err == nil || strings.Contains(err.Error(), key[:8]) {
			t.Fatalf("key again at %d: %v", second, err)
		}
	}
}

// TestStreamEnds: an answer is whole once the server says it ended, by [DONE]
// or a finish reason; one cut short, or one past maxAnswer, is an error.
func TestStreamEnds(t *testing.T) {
	chunk := func(s string) string { return `data: {"choices":[{"delta":{"content":"` + s + `"}}]}` + "\n\n" }
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"done":      {chunk("hi") + "data: [DONE]\n\n", true},
		"finished":  {`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\n", true},
		"cut short": {chunk("hi"), false},
		"flood":     {strings.Repeat(chunk(strings.Repeat("x", 1<<16)), 17) + "data: [DONE]\n\n", false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(tc.body))
		}))
		_, err := (&OpenAI{BaseURL: srv.URL, ModelName: "m"}).Generate(context.Background(), nil)
		srv.Close()
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestMeasure: a measured answer asks for the server's token counts and
// reports them with its characters; an unmeasured one asks for none.
func TestMeasure(t *testing.T) {
	var asked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked.Store(strings.Contains(string(body), `"include_usage":true`))
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"héllo"},"finish_reason":"stop"}]}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":8}}}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	var got []Usage
	c := &OpenAI{BaseURL: srv.URL, ModelName: "m", Measure: func(u Usage) { got = append(got, u) }}
	if _, err := c.Generate(context.Background(), []Message{{Role: "user", Content: "größe"}}); err != nil {
		t.Fatal(err)
	}
	if !asked.Load() || len(got) != 1 || got[0] != (Usage{InChars: 5, OutChars: 5, InTokens: 12, OutTokens: 3, CachedTokens: 8}) {
		t.Fatalf("asked %v, measured %+v", asked.Load(), got)
	}
	c.Measure = nil
	if _, err := c.Generate(context.Background(), nil); err != nil || asked.Load() {
		t.Fatalf("unmeasured: asked %v, %v", asked.Load(), err)
	}
}

// TestEmbedAnswerSize: an embeddings answer past 64 MB is refused as too
// large; a broken small one is a decode error.
func TestEmbedAnswerSize(t *testing.T) {
	for _, huge := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"data":[{"index":0,"embedding":[`))
			chunk := []byte(strings.Repeat("0.5,", 1<<14)) // 64 KiB
			for i := 0; huge && i <= 1<<10; i++ {
				w.Write(chunk)
			}
		}))
		_, err := (&OpenAI{BaseURL: srv.URL, ModelName: "m"}).Embed(context.Background(), []string{"x"})
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "passed 64 MB") != huge {
			t.Errorf("huge %v: %v", huge, err)
		}
	}
}

// TestEmbedOrder: vectors are placed by their index, whatever order the server
// lists them in; the key goes as a bearer token; a refused request (4xx) is
// not tried again.
func TestEmbedOrder(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}]}`))
	}))
	defer srv.Close()
	vecs, err := (&OpenAI{BaseURL: srv.URL, APIKey: "k", ModelName: "m"}).Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Fatalf("got %v %v", vecs, err)
	}
	if _, err := (&OpenAI{BaseURL: srv.URL, APIKey: "wrong", ModelName: "m"}).Embed(context.Background(), []string{"a"}); err == nil || calls.Load() != 2 {
		t.Fatalf("a refused key: %v, after %d requests in all", err, calls.Load())
	}
}

// TestThrottleForgetsAbandoned: callers that give up waiting for their turn
// leave no gap behind, so the next caller is not pushed past its timeout; turns
// stay spaced, and a caller that gave up before it came takes none.
func TestThrottleForgetsAbandoned(t *testing.T) {
	c := &OpenAI{RPM: 600} // a turn every 100ms
	stopped, stop := context.WithCancel(context.Background())
	stop()
	if c.throttle(stopped) == nil {
		t.Fatal("a caller that had given up took the free turn")
	}
	gone, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for range 20 { // one gets the first turn; the rest give up
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.throttle(gone)
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.throttle(ctx); err != nil {
		t.Fatalf("the caller after 19 abandoned ones: %v", err)
	}
	start := time.Now()
	if err := c.throttle(ctx); err != nil || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("the next turn came after %v: %v", time.Since(start), err)
	}
}

// TestStreamErrorMasked: an error the server sends inside the stream reaches the
// caller as one from a refused request does: key masked, at most 2048 bytes.
func TestStreamErrorMasked(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`data: {"error":{"message":"` + key + strings.Repeat("x", 5000) + `"}}` + "\n\n"))
	}))
	defer srv.Close()
	_, err := (&OpenAI{BaseURL: srv.URL, APIKey: key, ModelName: "m"}).Generate(context.Background(), nil)
	if err == nil || strings.Contains(err.Error(), key[:8]) || len(err.Error()) > 2048 {
		t.Fatalf("stream error: %.80v", err)
	}
}

// TestTimeoutNamed: a request that outlasts Timeout fails naming the model and
// DEEPCODE_LLM_TIMEOUT, not with a bare "context deadline exceeded", and still
// counts as a deadline.
func TestTimeoutNamed(t *testing.T) {
	answer := make(chan struct{}) // never, until the test ends (a handler that reads no body hears no disconnect)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-answer }))
	defer srv.Close()
	defer close(answer)
	c := &OpenAI{BaseURL: srv.URL, ModelName: "m", Timeout: 50 * time.Millisecond}
	_, chat := c.Generate(context.Background(), nil)
	_, embed := c.Embed(context.Background(), []string{"x"})
	for _, err := range []error{chat, embed} {
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "from m within DEEPCODE_LLM_TIMEOUT") {
			t.Fatalf("past the timeout: %v", err)
		}
	}
}

// TestRetryAfterBounded: a Retry-After too large to count in nanoseconds is
// waited out like any long one, not wrapped round into an instant retry.
func TestRetryAfterBounded(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "10000000000")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := (&OpenAI{BaseURL: srv.URL, ModelName: "m"}).Generate(ctx, nil); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("after %d requests: %v", calls.Load(), err)
	}
}
