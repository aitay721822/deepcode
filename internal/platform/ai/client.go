// Package ai is an OpenAI-compatible client for chat and embeddings, on the
// official SDK (openai-go), that works with OpenAI, llama.cpp, LM Studio,
// vLLM, Ollama and similar servers. A nil *OpenAI means the model is not
// configured. Streams, accumulation, tool and schema params are the SDK's;
// what this package adds is what the SDK leaves to its user: one order for
// every request's messages (Prompt), request options (no OPENAI_* environment,
// retries, RPM, body caps, store false on the Responses API), errors that
// name the model and mask the key, timeouts that
// name DEEPCODE_LLM_TIMEOUT, a Chat Completions stream with no finish reason
// taken as cut, the 1 MB answer cap, DeepSeek's cached-token count, and
// schemas, made from Go types or at request time within OpenAI's limits,
// checked on the answer.
package ai

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Prompt is a request's messages in the order every request sends them, so a
// model server's prompt cache holds the longest prefix requests share: Static,
// the instructions the same on every request of its kind, as the system
// message; Previous, the conversation so far; Dynamic, what is new to this
// request, as the last user message.
type Prompt struct {
	Static   string
	Previous []Message
	Dynamic  string
}

// messages are p's in that order, an empty Static or Dynamic left out.
func (p Prompt) messages() []Message {
	var out []Message
	if p.Static != "" {
		out = append(out, Message{Role: "system", Content: p.Static})
	}
	out = append(out, p.Previous...)
	if p.Dynamic != "" {
		out = append(out, Message{Role: "user", Content: p.Dynamic})
	}
	return out
}

// Size is p's messages in count's units, and how many there are.
func (p Prompt) Size(count func(string) int) (size, messages int) {
	for _, m := range p.messages() {
		size, messages = size+count(m.Content), messages+1
	}
	return size, messages
}

// After is the prompt that goes on from p once the model answered reply: p's
// Dynamic and reply join Previous, and dynamic is what is new.
func (p Prompt) After(reply, dynamic string) Prompt {
	previous := Prompt{Previous: p.Previous, Dynamic: p.Dynamic}.messages() // a copy: p's own stays as it was
	return Prompt{Static: p.Static, Previous: append(previous, Message{Role: "assistant", Content: reply}), Dynamic: dynamic}
}

// OpenAI talks to any server implementing /chat/completions or /responses,
// and /embeddings.
type OpenAI struct {
	BaseURL   string
	APIKey    string
	ModelName string
	API       string // Responses asks a chat model by the Responses API; else Completions
	Schema    string // SchemaPrompt writes a request's JSON schema in its prompt; else SchemaServer
	MaxTokens int    // 0 leaves the output limit to the server
	// ReasoningEffort is sent as reasoning_effort when set ("none", "low", ...);
	// empty leaves reasoning to the server's default.
	ReasoningEffort string
	Temperature     *float64      // sent as temperature when set; nil leaves it to the server
	Timeout         time.Duration // per request, including the whole stream
	RPM             int           // requests per minute, 0 = unlimited
	// Measure, when set, is told every answer's Usage, failed ones too, and a
	// Chat Completions server is asked for its token counts
	// (stream_options.include_usage), which the Responses API always gives. The
	// bench sets it; a server that rejects the option fails the request.
	Measure func(Usage)
	// Window is the model's context in tokens, 0 when not set (Context).
	// CompactRatio is the share of it a request may fill before it is made
	// smaller (package chat/harness); 0 means 0.8.
	Window       int
	CompactRatio float64

	mu      sync.Mutex
	next    time.Time
	learned int // the context a server reported (Learn)
}

// DefaultOutput is the room, in tokens, kept in the context for an answer
// whose size MaxTokens leaves to the server.
const DefaultOutput = 8192

// Room is the tokens kept in the context for the answer: MaxTokens, else
// DefaultOutput.
func (c *OpenAI) Room() int { return cmp.Or(c.MaxTokens, DefaultOutput) }

// Context is the model's context in tokens: Window, or the smaller context a
// server reported (Learn), or 0 when neither is known.
func (c *OpenAI) Context() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.learned > 0 && (c.Window == 0 || c.learned < c.Window) {
		return c.learned
	}
	return c.Window
}

// Learn keeps n, the context a server reported in an ErrContext, for as long
// as the process runs: no server reports it until a request passes it.
func (c *OpenAI) Learn(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > 0 {
		c.learned = n
	}
}

// ErrContext is a request the server refused as longer than the model's
// context: OpenAI's code context_length_exceeded, llama.cpp's type
// exceed_context_size_error. Prompt and Context are the request's tokens and
// the context's when the server gives them, as llama.cpp does, else 0.
type ErrContext struct {
	Prompt, Context int
	err             error // the refusal as fail words it
}

func (e *ErrContext) Error() string {
	if e.err == nil {
		return "the request is longer than the model's context"
	}
	return e.err.Error()
}

// overflow is err, a refusal whose body is body, as an ErrContext when the body
// says the request was longer than the context; else err. Other servers' words
// for it are added once seen, not guessed.
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
	return &ErrContext{Prompt: b.Error.Prompt, Context: b.Error.Context, err: err}
}

// maxBody bounds an answer's body: a batch's vectors are some megabytes, a
// streamed answer less; a server sending far more is broken.
const maxBody = 64 << 20

// options are every request's: the server and its key and nothing from the
// environment, as the SDK's services read none (openai.NewClient would send
// OPENAI_API_KEY to whatever server this is); rate limits, server errors and
// network failures tried 3 times more with backoff, a Retry-After honoured up
// to a minute; and middleware.
func (c *OpenAI) options() []option.RequestOption {
	return []option.RequestOption{
		option.WithBaseURL(c.BaseURL), option.WithAPIKey(c.APIKey),
		option.WithMaxRetries(3), option.WithMaxRetryDelay(time.Minute),
		option.WithMiddleware(c.middleware),
	}
}

// middleware gives each attempt its turn under RPM (throttle) and bounds the
// answer's body by maxBody.
func (c *OpenAI) middleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
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
// request refused as longer than the context is an ErrContext.
func (c *OpenAI) fail(path string, res *http.Response, err error) error {
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
		if msg := c.quote(body); msg != "" {
			return overflow(body, fmt.Errorf("%s %s: %s: %s", c.ModelName, path, res.Status, msg))
		}
		return fmt.Errorf("%s %s: %s", c.ModelName, path, res.Status)
	case errors.As(err, &stream):
		return overflow(stream.Event.Data, c.refused(path, stream.Message))
	}
	return fmt.Errorf("%s %s: %w", c.ModelName, path, err)
}

// ErrFailed: the server failed the answer it was giving (an error inside the
// stream, a failed response) and sent none of its text, as Strata does when
// the structured answer it holds back fails its check: the model's or the
// server's failure, not a feature the server lacks.
var ErrFailed = errors.New("the server failed the answer")

// answerFailed is a server's message that the answer failed (refused), an
// ErrFailed.
type answerFailed struct{ error }

func (answerFailed) Is(target error) bool { return target == ErrFailed }

// refused is the error for a server's message that the answer failed, quoted.
func (c *OpenAI) refused(path, msg string) error {
	return answerFailed{fmt.Errorf("%s %s: %s", c.ModelName, path, c.quote([]byte(cmp.Or(msg, "the answer failed, the server saying no more"))))}
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

// Embed returns one vector per text, in input order.
func (c *OpenAI) Embed(ctx context.Context, texts []string) (_ [][]float32, err error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	defer func() { err = cause(ctx, err) }() // before cancel, which would end ctx too
	var raw *http.Response
	svc := openai.NewEmbeddingService(c.options()...)
	res, err := svc.New(ctx, openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(c.ModelName),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	}, option.WithResponseInto(&raw))
	if err != nil {
		return nil, c.fail("/embeddings", raw, err)
	}
	vecs := make([][]float32, len(texts))
	for _, d := range res.Data {
		if d.Index < 0 || d.Index >= int64(len(vecs)) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		v := make([]float32, len(d.Embedding))
		for i, x := range d.Embedding {
			v[i] = float32(x)
		}
		vecs[d.Index] = v
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("no embedding returned for input %d", i)
		}
	}
	return vecs, nil
}
