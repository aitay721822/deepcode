package ai

import (
	"context"
	"errors"
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
