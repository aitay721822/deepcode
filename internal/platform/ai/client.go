// Package ai is an OpenAI-compatible client for chat and embeddings that
// works with OpenAI, llama.cpp, LM Studio, vLLM, Ollama and similar servers.
// A nil *OpenAI means the model is not configured.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAI talks to any server implementing /chat/completions and /embeddings.
type OpenAI struct {
	BaseURL   string
	APIKey    string
	ModelName string
	MaxTokens int // 0 leaves the output limit to the server
	// ReasoningEffort is sent as reasoning_effort when set ("none", "low", ...);
	// empty leaves reasoning to the server's default.
	ReasoningEffort string
	Timeout         time.Duration // per request, including the whole stream
	RPM             int           // requests per minute, 0 = unlimited
	// Measure, when set, is told every answer's Usage, failed ones too, and the
	// server is asked for its token counts (stream_options.include_usage). The
	// bench sets it; a server that rejects the option fails the request.
	Measure func(Usage)

	mu   sync.Mutex
	next time.Time
}

// post retries rate limits, server errors and network failures with exponential
// backoff (honouring Retry-After) until a 200 response arrives.
func (c *OpenAI) post(ctx context.Context, path string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var lastErr error
	wait := time.Second
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		// Reading a key's length past the 2048 bytes quote keeps gets a key that
		// crosses the cut whole, so it is masked too.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048+int64(len(c.APIKey))))
		resp.Body.Close()
		lastErr = fmt.Errorf("%s %s: %s: %s", c.ModelName, path, resp.Status, c.quote(msg))
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return nil, lastErr
		}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			wait = time.Duration(min(s, 60)) * time.Second // in nanoseconds, a huge value would wrap round to no wait
		}
	}
	return nil, lastErr
}

// quote is a server's message fit for an error: the API key masked, since a
// server may repeat the key it refused, and at most 2048 bytes. The mask keeps
// the length, so the cut never leaves part of a key.
func (c *OpenAI) quote(msg []byte) string {
	if c.APIKey != "" {
		msg = bytes.ReplaceAll(msg, []byte(c.APIKey), bytes.Repeat([]byte("*"), len(c.APIKey)))
	}
	return strings.ToValidUTF8(string(bytes.TrimSpace(msg[:min(len(msg), 2048)])), "")
}

// withTimeout bounds one request by Timeout. Its cause names the model and the
// setting, which a bare "context deadline exceeded" in the log or in a job's
// error would not; cause hands it on.
func (c *OpenAI) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, c.Timeout,
		fmt.Errorf("no whole answer from %s within DEEPCODE_LLM_TIMEOUT (%s): %w", c.ModelName, c.Timeout, context.DeadlineExceeded))
}

// cause is err, or once ctx has ended, why it ended: the request's own
// timeout, or the caller's reason (a job's timeout, a reader who left).
func cause(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

// throttle spaces requests evenly to stay under RPM. A caller takes its turn
// only once it comes: turns booked ahead would stay booked when their callers
// give up, so a burst of abandoned calls (search queries past their timeout)
// would push every later call past its own, long after the burst.
func (c *OpenAI) throttle(ctx context.Context) error {
	if c.RPM <= 0 {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err // a caller that has given up takes no turn, not even a free one
		}
		c.mu.Lock()
		wait := time.Until(c.next)
		if wait <= 0 {
			c.next = time.Now().Add(time.Minute / time.Duration(c.RPM))
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
