// Package compat is an ai.Chat on an OpenAI-compatible server, and an
// embeddings client, on the official SDK (openai-go), that works with OpenAI,
// llama.cpp, LM Studio, vLLM, Ollama and similar servers. A nil *Client means
// the model is not configured. Streams, accumulation, tool and schema params
// are the SDK's; what this package adds is what the SDK leaves to its user:
// request options (no OPENAI_* environment, retries, RPM, body caps, store
// false on the Responses API), errors that name the model, mask the key and
// say whether a later try may get through (ai.ErrUnavailable, ai.ErrRefused),
// a Chat Completions stream with no finish reason taken as cut, and
// DeepSeek's cached-token count.
package compat

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/usage"
)

// Client talks to any server implementing /chat/completions or /responses,
// and /embeddings.
type Client struct {
	BaseURL   string
	APIKey    string
	ModelName string
	API       string // Responses asks a chat model by the Responses API; else Completions
	Schema    string // ai.SchemaPrompt writes a request's JSON schema in its prompt; else ai.SchemaServer
	MaxTokens int    // 0 leaves the output limit to the server
	// ReasoningEffort is sent as reasoning_effort when set ("none", "low", ...);
	// empty leaves reasoning to the server's default.
	ReasoningEffort string
	Temperature     *float64      // sent as temperature when set; nil leaves it to the server
	Timeout         time.Duration // per request, including the whole stream
	RPM             int           // requests per minute, 0 = unlimited
	// Meter, when set, is told every request's Usage, failed ones too, and a
	// Chat Completions server is asked for its token counts
	// (stream_options.include_usage), which the Responses API always gives. The
	// server keeps a usage.Manager here, the bench its own; a server that
	// rejects the option fails the request.
	Meter usage.Meter
	// Count estimates the tokens of a chat model's answer the server counted
	// none for (usage.Usage.Estimate); nil estimates none. A failed request,
	// and an embedding, has none estimated.
	Count func(string) int
	// Context is the model's context in tokens, 0 when not set (Window).
	// CompactRatio is the share of it a request may fill before it is made
	// smaller (ai.Window.Ratio); 0 leaves it to the caller's default.
	Context      int
	CompactRatio float64

	mu      sync.Mutex
	next    time.Time
	learned int // the context a server reported (Learn)
}

// Name is the model's name.
func (c *Client) Name() string { return c.ModelName }

// SchemaMode is ai.SchemaPrompt when Schema says so, else ai.SchemaServer.
func (c *Client) SchemaMode() string {
	if c.Schema == ai.SchemaPrompt {
		return ai.SchemaPrompt
	}
	return ai.SchemaServer
}

// Window is the model's context: Context, or the smaller context a server
// reported (Learn), or 0 when neither is known; the room kept for the answer,
// MaxTokens, else ai.DefaultOutput; and CompactRatio. A server adds nothing to
// what it is sent, and its tokens are the ones counted: Overhead 0, Scale 1.
func (c *Client) Window() ai.Window {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := ai.Window{Context: c.Context, Room: cmp.Or(c.MaxTokens, ai.DefaultOutput), Ratio: c.CompactRatio, Scale: 1}
	if c.learned > 0 && (c.Context == 0 || c.learned < c.Context) {
		w.Context = c.learned
	}
	return w
}

// Learn keeps n, the context a server reported in an ai.ErrContext, for as
// long as the process runs: no server reports it until a request passes it.
func (c *Client) Learn(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > 0 {
		c.learned = n
	}
}

// overflow is err, a refusal whose body is body, as an ai.ErrContext when the
// body says the request was longer than the context: OpenAI's code
// context_length_exceeded, llama.cpp's type exceed_context_size_error, with
// the request's tokens and the context's when it gives them; else err. Other
// servers' words for it are added once seen, not guessed.
func overflow(body []byte, err error) error {
	var b struct {
		Error struct {
			Code    json.RawMessage `json:"code"`
			Type    string          `json:"type"`
			Prompt  int             `json:"n_prompt_tokens"`
			Context int             `json:"n_ctx"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &b) != nil || string(b.Error.Code) != `"context_length_exceeded"` && b.Error.Type != "exceed_context_size_error" {
		return err
	}
	return &ai.ErrContext{Prompt: b.Error.Prompt, Context: b.Error.Context, Err: err}
}

// maxBody bounds an answer's body: a batch's vectors are some megabytes, a
// streamed answer less; a server sending far more is broken.
const maxBody = 64 << 20

// options are every request's: the server and its key and nothing from the
// environment, as the SDK's services read none (openai.NewClient would send
// OPENAI_API_KEY to whatever server this is); rate limits, server errors and
// network failures tried 3 times more with backoff, a Retry-After honoured up
// to a minute; and middleware.
func (c *Client) options() []option.RequestOption {
	return []option.RequestOption{
		option.WithBaseURL(c.BaseURL), option.WithAPIKey(c.APIKey),
		option.WithMaxRetries(3), option.WithMaxRetryDelay(time.Minute),
		option.WithMiddleware(c.middleware),
	}
}

// middleware gives each attempt its turn under RPM (throttle) and bounds the
// answer's body by maxBody.
func (c *Client) middleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	if err := c.throttle(req.Context()); err != nil {
		return nil, err
	}
	res, err := next(req)
	if err == nil {
		res.Body = http.MaxBytesReader(nil, res.Body, maxBody)
	}
	return res, err
}

// fail is err fit to return, naming the model and path it came from; res is
// the answer err came with, if any (option.WithResponseInto). A refused
// request's error carries its status and the server's message (quote): the
// SDK's names the status alone, and for a message it cannot read as OpenAI's
// ({"error":"model not loaded"}, as LM Studio sends) it is a JSON error with
// neither. An error the server sends inside a stream is quoted as well. A
// request refused as longer than the context is an ai.ErrContext. One a later
// try may get through is an ai.ErrUnavailable: the connection failed or broke
// off (broken), or the server answered 408, 409, 429 or a 5xx once the SDK's
// own retries were spent. One refused for a wrong setting is an
// ai.ErrRefused: the key (401, 403), no such model or path (404), or OpenAI's
// account out of credit (a 429 of insufficient_quota).
func (c *Client) fail(path string, res *http.Response, err error) error {
	var api *openai.Error
	var stream *ssestream.StreamError
	var big *http.MaxBytesError
	if errors.As(err, &api) && api.Response != nil {
		res = api.Response
	}
	switch {
	case errors.As(err, &big):
		return fmt.Errorf("%s %s: the answer passed 64 MB", c.ModelName, path)
	case res != nil && res.StatusCode >= 400:
		// The SDK has read the body and put it back. Reading a key's length past
		// the 2048 bytes quote keeps gets a key that crosses the cut whole, so it
		// is masked too.
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048+int64(len(c.APIKey))))
		refusal := fmt.Errorf("%s %s: %s", c.ModelName, path, res.Status)
		if msg := c.quote(body); msg != "" {
			refusal = overflow(body, fmt.Errorf("%s %s: %s: %s", c.ModelName, path, res.Status, msg))
		}
		switch s := res.StatusCode; {
		case errors.As(refusal, new(*ai.ErrContext)):
			return refusal // longer than the context, whatever the status: no later try is shorter
		case s == http.StatusTooManyRequests && bytes.Contains(body, []byte(`"insufficient_quota"`)):
			return ai.Kinded(refusal, ai.ErrRefused) // OpenAI's account out of credit: no wait ends it
		case s == http.StatusRequestTimeout || s == http.StatusConflict || s == http.StatusTooManyRequests || s >= 500:
			return ai.Kinded(refusal, ai.ErrUnavailable)
		case s == http.StatusUnauthorized || s == http.StatusForbidden || s == http.StatusNotFound:
			return ai.Kinded(refusal, ai.ErrRefused)
		}
		return refusal
	case errors.As(err, &stream):
		return overflow(stream.Event.Data, c.refused(path, stream.Message))
	}
	if broken(err) {
		return ai.Kinded(fmt.Errorf("%s %s: %w", c.ModelName, path, err), ai.ErrUnavailable)
	}
	return fmt.Errorf("%s %s: %w", c.ModelName, path, err)
}

// broken reports a request that got no whole answer for want of a working
// connection: one that failed or broke off (a dial, read or write error, an
// end midway, a stream's included), or timed out below the caller's context.
// A certificate the client does not trust, a TLS alert the server sent (its
// refusal of the client's certificate), or a malformed URL, is no such
// failure: a later try meets it again.
func broken(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if op := (*net.OpError)(nil); errors.As(err, &op) {
		return op.Op != "remote error" // crypto/tls's name for an alert the peer sent
	}
	var ne net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &ne) && ne.Timeout()
}

// refused is the error for a server's message that the answer failed, quoted:
// an ai.ErrFailed, as Strata sends when the structured answer it holds back
// fails its check.
func (c *Client) refused(path, msg string) error {
	return ai.Kinded(fmt.Errorf("%s %s: %s", c.ModelName, path, c.quote([]byte(cmp.Or(msg, "the answer failed, the server saying no more")))), ai.ErrFailed)
}

// quote is a server's message fit for an error: the API key masked, since a
// server may repeat the key it refused, and at most 2048 bytes. The mask keeps
// the length, so the cut never leaves part of a key.
func (c *Client) quote(msg []byte) string {
	if c.APIKey != "" {
		msg = bytes.ReplaceAll(msg, []byte(c.APIKey), bytes.Repeat([]byte("*"), len(c.APIKey)))
	}
	return strings.ToValidUTF8(string(bytes.TrimSpace(msg[:min(len(msg), 2048)])), "")
}

// throttle spaces requests evenly to stay under RPM. A caller takes its turn
// only once it comes: turns booked ahead would stay booked when their callers
// give up, so a burst of abandoned calls (search queries past their timeout)
// would push every later call past its own, long after the burst.
func (c *Client) throttle(ctx context.Context) error {
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
