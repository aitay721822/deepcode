package compat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/usage"
)

// serve is a server writing body to every request, with status unless 0.
func serve(t *testing.T, status int, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// untimed is u with its time left out, to compare.
func untimed(u ai.Usage) ai.Usage {
	u.Elapsed = 0
	return u
}

// A server that repeats the key it refused gets none of it into the error: not whole, not where the 2048-byte cut
// runs through it, not past the cut. The status and the server's words are kept.
func TestRefusedKeyMasked(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	// The key at the start, and a second copy at offset second: before, across and past the cut.
	for _, second := range []int{100, 2048 - len(key)/2, 2050} {
		srv := serve(t, http.StatusUnauthorized, key+strings.Repeat("x", second-len(key))+key+" is not a valid key")
		_, err := ai.Generate(context.Background(), &Client{BaseURL: srv.URL, APIKey: key, ModelName: "m"}, ai.Prompt{})
		if err == nil || strings.Contains(err.Error(), key[:8]) || !strings.Contains(err.Error(), "m /chat/completions: 401 Unauthorized: ****") {
			t.Fatalf("key again at %d: %.120v", second, err)
		}
	}
}

// TestRefusedMessage: a refused request's error names the model, the path and the status, and quotes the
// server's words whatever their shape, on each endpoint: OpenAI's error object, a bare string as LM Studio
// sends, nothing; as an ai.ErrContext, with any counts, when longer than the context (llama.cpp's, OpenAI's).
func TestRefusedMessage(t *testing.T) {
	for _, tc := range []struct {
		body, want string
		over       *ai.ErrContext // nil: no ai.ErrContext
	}{
		{`{"error":{"message":"no such model","type":"invalid_request_error"}}`, `400 Bad Request: {"error":{"message":"no such model"`, nil},
		{`{"error":"model not loaded"}`, `400 Bad Request: {"error":"model not loaded"}`, nil},
		{"", "400 Bad Request", nil},
		{`{"error":{"code":400,"message":"request (9000 tokens) exceeds the available context size (8192 tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":9000,"n_ctx":8192}}`, `400 Bad Request: {"error":{"code":400,"message":"request (9000`, &ai.ErrContext{Prompt: 9000, Context: 8192}},
		{`{"error":{"message":"This model's maximum context length is 8192 tokens.","type":"invalid_request_error","param":"messages","code":"context_length_exceeded"}}`, `400 Bad Request: {"error":{"message":"This model's`, &ai.ErrContext{}},
	} {
		c := &Client{BaseURL: serve(t, http.StatusBadRequest, tc.body).URL, ModelName: "m"}
		_, chat := ai.Generate(context.Background(), c, ai.Prompt{})
		_, embed := c.Embed(context.Background(), []string{"x"})
		c.API = Responses
		_, responses := ai.Generate(context.Background(), c, ai.Prompt{})
		for path, err := range map[string]error{"/chat/completions": chat, "/embeddings": embed, "/responses": responses} {
			if over, want := (*ai.ErrContext)(nil), "m "+path+": "+tc.want; err == nil || !strings.HasPrefix(err.Error(), want) || tc.body == "" && err.Error() != want ||
				errors.As(err, &over) != (tc.over != nil) || over != nil && (over.Prompt != tc.over.Prompt || over.Context != tc.over.Context) {
				t.Errorf("%s, %s: %v %+v", tc.body, path, err, over)
			}
		}
	}
}

// TestNoKeyFromEnvironment: a server given no key gets none, not the
// OPENAI_API_KEY of the environment, nor goes to its OPENAI_BASE_URL.
func TestNoKeyFromEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Write([]byte(finished))
	}))
	defer srv.Close()
	if _, err := ai.Generate(context.Background(), &Client{BaseURL: srv.URL + "/v1", ModelName: "m"}, ai.Prompt{}); err != nil || auth.Load() != "" {
		t.Fatalf("authorization %q: %v", auth.Load(), err)
	}
}

// finished ends a Chat Completions stream as servers do: a finish reason, then [DONE].
const finished = `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"

// TestStreamEnds: an answer is whole once the server gave its finish reason, [DONE] or not; one without, even
// ended by [DONE], was cut short, and is an error, as is one past maxAnswer; one stopped by the output budget
// with nothing said says so.
func TestStreamEnds(t *testing.T) {
	chunk := func(s string) string { return `data: {"choices":[{"delta":{"content":"` + s + `"}}]}` + "\n\n" }
	for name, tc := range map[string]struct {
		body, fails string
	}{
		"finished":   {chunk("hi") + finished, ""},
		"no [DONE]":  {`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\n", ""},
		"at length":  {`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"length"}]}` + "\n\n", ""},
		"no answer":  {`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n", "whole output budget"},
		"[DONE] cut": {chunk("hi") + "data: [DONE]\n\n", "ended before it was finished"},
		"cut short":  {chunk("hi"), "ended before it was finished"},
		"flood":      {strings.Repeat(chunk(strings.Repeat("x", 1<<16)), 17) + finished, "passed 1 MB"},
	} {
		got, err := ai.Generate(context.Background(), &Client{BaseURL: serve(t, 0, tc.body).URL, ModelName: "m"}, ai.Prompt{})
		if tc.fails == "" && (err != nil || got != "hi") || tc.fails != "" && (err == nil || !strings.Contains(err.Error(), tc.fails)) {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
}

// indented is the line that closes every request for JSON (ai.Format.Prompt).
const indented = "Write the JSON indented, each key on a line of its own."

// TestFormat: StreamJSON asks for the schema made from a Go type, strict, on each API, its instructions ending
// with indented, and reads the answer into it; Generate asks for neither. An answer sent that is not that JSON
// fails with ai.ErrSchema, naming what the endpoint must support; a request the server refuses fails with its
// words, as any other, not ai.ErrSchema; JSON cut at the output budget says so instead.
func TestFormat(t *testing.T) {
	type page struct {
		Title string   `json:"title"`
		Files []string `json:"files"`
	}
	f := ai.Schema[struct {
		Pages []page `json:"pages"`
	}]("plan")
	var body, mode atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		text, finish, end := `{"pages":[{"title":"A","files":["a.go"]}]}`, "stop", `{"type":"response.completed","response":{}}`
		switch mode.Load() {
		case "refused":
			http.Error(w, `{"error":{"message":"response_format is not supported"}}`, http.StatusBadRequest)
			return
		case "prose":
			text = "Sure, here is the plan."
		case "shape":
			text = `{"pages":[{"title":"A"}]}` // JSON, but not the schema's: a page lacks its files
		case "cut":
			text, finish, end = `{"pages":[{"title":"A"`, "length", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": text}, "finish_reason": finish}}})
		delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": text})
		fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: %s\n\n", chunk, delta, end) // Chat Completions reads the first, Responses the others
	}))
	defer srv.Close()
	schema := `"schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"pages":{"items":{"properties":` +
		`{"title":{"type":"string"},"files":{"items":{"type":"string"},"type":"array"}},"additionalProperties":false,"type":"object","required":["title","files"]},` +
		`"type":"array"}},"required":["pages"],"type":"object"}`
	for api, want := range map[string]string{
		Completions: `"response_format":{"json_schema":{"name":"plan","strict":true,` + schema + `},"type":"json_schema"}`,
		Responses:   `"text":{"format":{"name":"plan",` + schema + `,"strict":true,"type":"json_schema"}}`,
	} {
		c := &Client{BaseURL: srv.URL, ModelName: "m", API: api}
		mode.Store("")
		if v, _, err := ai.StreamJSON(context.Background(), c, ai.Prompt{Static: "S"}, f, nil); err != nil || len(v.Pages) != 1 || v.Pages[0].Files[0] != "a.go" || !strings.Contains(body.Load().(string), want) || !strings.Contains(body.Load().(string), `"S\n`+indented+`"`) {
			t.Errorf("%s: %+v %v\n%s", api, v, err, body.Load())
		}
		if _, err := ai.Generate(context.Background(), c, ai.Prompt{}); err != nil || strings.Contains(body.Load().(string), "schema") || strings.Contains(body.Load().(string), indented) {
			t.Errorf("%s, no format: %v\n%s", api, err, body.Load())
		}
		inPrompt := &Client{BaseURL: srv.URL, ModelName: "m", API: api, Schema: ai.SchemaPrompt} // the schema in the prompt alone
		if _, _, err := ai.StreamJSON(context.Background(), inPrompt, ai.Prompt{}, f, nil); err != nil || strings.Contains(body.Load().(string), `"json_schema"`) ||
			!strings.Contains(body.Load().(string), ai.SchemaLine) {
			t.Errorf("%s, schema in the prompt: %v\n%s", api, err, body.Load())
		}
		for m, says := range map[string]string{"prose": "m: the answer is not the JSON of plan", "shape": "m: the answer is not the JSON of plan", "refused": "400 Bad Request: {\"error\":{\"message\":\"response_format is not supported\"}}",
			"cut": "m: the answer reached the output budget before its JSON ended"} {
			mode.Store(m)
			_, _, err := ai.StreamJSON(context.Background(), c, ai.Prompt{}, f, nil)
			schema := m == "prose" || m == "shape" // an answer sent (200) that is not the schema's JSON
			if err == nil || !strings.Contains(err.Error(), says) || errors.Is(err, ai.ErrSchema) != schema || schema && !strings.HasSuffix(err.Error(), ai.ErrSchema.Error()) || !strings.Contains(body.Load().(string), `"`+indented+`"`) {
				t.Errorf("%s, %s: %v", api, m, err)
			}
		}
	}
}

// TestResponses: by the Responses API an answer is the output text's deltas, whole once the response completed
// or ended incomplete; a failed response, an error event and a stream cut short are errors, and an answer that
// spent the whole budget on reasoning says so. The request asks for nothing stored. An answer the server counted
// no tokens for is metered with its tokens estimated by Count; one it counted for, or that failed, with none.
func TestResponses(t *testing.T) {
	ev := func(typ, rest string) string { return `data: {"type":"` + typ + `"` + rest + "}\n\n" }
	delta := ev("response.output_text.delta", `,"delta":"hi"`)
	counts := `,"usage":{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":8}}`
	for name, tc := range map[string]struct {
		body, want, fails string
	}{
		"completed":  {delta + ev("response.completed", `,"response":{"status":"completed"`+counts+`}`), "hi", ""},
		"incomplete": {delta + ev("response.incomplete", `,"response":{"incomplete_details":{"reason":"max_output_tokens"}}`), "hi", ""},
		"no answer":  {ev("response.incomplete", `,"response":{"incomplete_details":{"reason":"max_output_tokens"}}`), "", "whole output budget"},
		"failed":     {delta + ev("response.failed", `,"response":{"error":{"code":"server_error","message":"it broke"}}`), "", "m /responses: it broke"},
		"no reason":  {ev("response.failed", `,"response":{}`), "", "the answer failed"},
		"error":      {ev("error", `,"code":"x","message":"no such model"`), "", "m /responses: no such model"},
		"cut short":  {delta, "", "ended before it was finished"},
	} {
		var body string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			body = r.URL.Path + " " + string(b)
			w.Write([]byte(tc.body))
		}))
		var got []ai.Usage
		c := &Client{BaseURL: srv.URL + "/v1", ModelName: "m", API: Responses, MaxTokens: 50, Count: utf8.RuneCountInString,
			Meter: usage.Func(func(_ context.Context, _ string, u ai.Usage) { got = append(got, untimed(u)) })}
		answer, err := ai.Generate(context.Background(), c, ai.Prompt{Static: "be brief", Dynamic: "größe"})
		srv.Close()
		if answer != tc.want && tc.fails == "" || tc.fails != "" && (err == nil || !strings.Contains(err.Error(), tc.fails)) || tc.fails == "" && err != nil {
			t.Errorf("%s: %q %v", name, answer, err)
		}
		for _, w := range []string{"/v1/responses ", `"store":false`, `"max_output_tokens":50`, `"role":"system"`, `"content":"be brief"`, `"content":"größe"`, `"stream":true`} {
			if !strings.Contains(body, w) {
				t.Errorf("%s: the request lacks %s: %s", name, w, body)
			}
		}
		if name == "completed" && (len(got) != 1 || got[0] != (ai.Usage{InChars: 13, OutChars: 2, InTokens: 12, OutTokens: 3, CachedTokens: 8})) ||
			name == "incomplete" && (len(got) != 1 || got[0] != (ai.Usage{InChars: 13, OutChars: 2, EstIn: 13, EstOut: 2})) ||
			name == "failed" && (len(got) != 1 || got[0] != (ai.Usage{InChars: 13, OutChars: 2})) {
			t.Errorf("measured %+v", got)
		}
	}
}

// TestMeasure: a metered answer asks for the server's token counts and tells the Meter them with its characters,
// the reasoning not counted, DeepSeek's cached tokens read (package usage), its time, its model and its
// context's repository; an unmetered one asks for none.
func TestMeasure(t *testing.T) {
	var asked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked.Store(strings.Contains(string(body), `"include_usage":true`))
		time.Sleep(20 * time.Millisecond)
		w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"hmm, "}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"content":"héllo"},"finish_reason":"stop"}]}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_cache_hit_tokens":9,"prompt_tokens_details":{"cached_tokens":8}}}` +
			"\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	var got []string
	c := &Client{BaseURL: srv.URL, ModelName: "m", Meter: usage.Func(func(ctx context.Context, model string, u ai.Usage) {
		got = append(got, fmt.Sprint(usage.Repository(ctx), model, untimed(u), u.Elapsed >= 20*time.Millisecond))
	})}
	if answer, err := ai.Generate(usage.WithRepository(context.Background(), 7), c, ai.Prompt{Dynamic: "größe"}); err != nil || answer != "héllo" {
		t.Fatalf("%q %v", answer, err)
	}
	if want := fmt.Sprint(7, "m", ai.Usage{InChars: 5, OutChars: 5, InTokens: 12, OutTokens: 3, CachedTokens: 9}, true); !asked.Load() || len(got) != 1 || got[0] != want {
		t.Fatalf("asked %v, measured %q, want %q", asked.Load(), got, want)
	}
	c.Meter = nil
	if _, err := ai.Generate(context.Background(), c, ai.Prompt{}); err != nil || asked.Load() {
		t.Fatalf("unmeasured: asked %v, %v", asked.Load(), err)
	}
}

// TestEmbedAnswerSize: an embeddings answer past 64 MB is refused as too
// large; a broken small one is a decode error.
func TestEmbedAnswerSize(t *testing.T) {
	for _, huge := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"index":0,"embedding":[`))
			chunk := []byte(strings.Repeat("0.5,", 1<<14)) // 64 KiB
			for i := 0; huge && i <= 1<<10; i++ {
				w.Write(chunk)
			}
		}))
		_, err := (&Client{BaseURL: srv.URL, ModelName: "m"}).Embed(context.Background(), []string{"x"})
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "passed 64 MB") != huge {
			t.Errorf("huge %v: %v", huge, err)
		}
	}
}

// TestEmbedOrder: vectors are placed by their index, whatever order the server lists them in, and their tokens
// metered; the key goes as a bearer token; a refused request (4xx) is not tried again, and is metered with none.
func TestEmbedOrder(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`))
	}))
	defer srv.Close()
	var got []ai.Usage
	meter := usage.Func(func(_ context.Context, _ string, u ai.Usage) { got = append(got, untimed(u)) })
	vecs, err := (&Client{BaseURL: srv.URL, APIKey: "k", ModelName: "m", Meter: meter}).Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 2 || len(got) != 1 || got[0] != (ai.Usage{InTokens: 4}) {
		t.Fatalf("got %v %v, metered %+v", vecs, err, got)
	}
	if _, err := (&Client{BaseURL: srv.URL, APIKey: "wrong", ModelName: "m", Meter: meter}).Embed(context.Background(), []string{"a"}); err == nil ||
		calls.Load() != 2 || len(got) != 2 || got[1] != (ai.Usage{}) {
		t.Fatalf("a refused key: %v, after %d requests in all, metered %+v", err, calls.Load(), got)
	}
}

// TestStreamErrorMasked: an error the server sends inside the stream reaches the caller as one from a refused
// request does: naming the model and path, the key masked, the server's words at most 2048 bytes; as an
// ai.ErrContext when longer than the context, as llama.cpp's server sent it before it answered 400.
func TestStreamErrorMasked(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	body := `data: {"error":{"type":"exceed_context_size_error","n_prompt_tokens":9,"n_ctx":8,"message":"` + key + strings.Repeat("x", 5000) + `"}}` + "\n\n"
	_, err := ai.Generate(context.Background(), &Client{BaseURL: serve(t, 0, body).URL, APIKey: key, ModelName: "m"}, ai.Prompt{})
	if over, from := (*ai.ErrContext)(nil), "m /chat/completions: "; err == nil || strings.Contains(err.Error(), key[:8]) || !strings.HasPrefix(err.Error(), from) || len(err.Error()) > len(from)+2048 || !errors.As(err, &over) || *over != (ai.ErrContext{Prompt: 9, Context: 8, Err: over.Err}) {
		t.Fatalf("stream error: %.80v", err)
	}
}

// TestTimeoutNamed: a request that outlasts Timeout fails naming the model and DEEPCODE_LLM_TIMEOUT, not with a
// bare "context deadline exceeded", and still counts as a deadline.
func TestTimeoutNamed(t *testing.T) {
	answer := make(chan struct{}) // never, until the test ends (a handler that reads no body hears no disconnect)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-answer }))
	defer srv.Close()
	defer close(answer)
	c := &Client{BaseURL: srv.URL, ModelName: "m", Timeout: 50 * time.Millisecond}
	_, chat := ai.Generate(context.Background(), c, ai.Prompt{})
	_, embed := c.Embed(context.Background(), []string{"x"})
	c.API = Responses
	_, responses := ai.Generate(context.Background(), c, ai.Prompt{})
	for _, err := range []error{chat, embed, responses} {
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "from m within DEEPCODE_LLM_TIMEOUT") {
			t.Fatalf("past the timeout: %v", err)
		}
	}
}

// TestRetries: a server error is tried again, and a rate limit once the Retry-After it names has passed; a refused
// request is not, nor a rate limit whose Retry-After passes a minute, which fails at once.
func TestRetries(t *testing.T) {
	for name, tc := range map[string]struct {
		status     int
		retryAfter string
		calls      int32
		wait       time.Duration // at least
	}{
		"server error":         {http.StatusBadGateway, "", 2, 0},
		"rate limit":           {http.StatusTooManyRequests, "1", 2, time.Second},
		"refused":              {http.StatusBadRequest, "", 1, 0},
		"rate limit past 1min": {http.StatusTooManyRequests, "10000000000", 1, 0},
	} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) > 1 {
				w.Write([]byte(finished))
				return
			}
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.status)
		}))
		start := time.Now()
		_, err := ai.Generate(context.Background(), &Client{BaseURL: srv.URL, ModelName: "m"}, ai.Prompt{})
		took := time.Since(start)
		srv.Close()
		if (err == nil) != (tc.calls == 2) || calls.Load() != tc.calls || took < tc.wait || took > 5*time.Second {
			t.Errorf("%s: %d requests in %v: %v", name, calls.Load(), took, err)
		}
	}
}

// TestExchange: on each API the tools go out strict, with their schema; a reply's calls come back in order,
// each with its own key though the server gave both one id, and the next request holds the reply and each
// result under its call's id (a result compacted by key, its shorter text in place), measured as sent; Size
// counts each piece once; a reply cut at the output budget is an error.
func TestExchange(t *testing.T) {
	type args struct {
		Query string `json:"query"`
	}
	tool := ai.Schema[args]("search").Tool("Search the code")
	replies := map[string][]string{
		Completions: {`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"looking","tool_calls":[` +
			`{"id":"c1","type":"function","function":{"name":"search","arguments":"{\"query\":\"router\"}"}},` +
			`{"id":"c1","type":"function","function":{"name":"done","arguments":""}}]}}]}`,
			`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"enough"}}]}`,
			`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"","tool_calls":[{"id":"c3","type":"function","function":{"name":"search","arguments":"{\"qu"}}]}}]}`},
		Responses: {`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"looking"}]},` +
			`{"type":"function_call","id":"fc1","call_id":"c1","name":"search","arguments":"{\"query\":\"router\"}"},` +
			`{"type":"function_call","id":"fc2","call_id":"c1","name":"done","arguments":""}]}`,
			`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"enough"}]}]}`,
			`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","call_id":"c3","name":"search","arguments":"{\"qu"}]}`},
	}
	for api, bodies := range replies {
		var sent []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(bodies[len(sent)]))
			sent = append(sent, string(b))
		}))
		var measured []int
		e := (&Client{BaseURL: srv.URL, ModelName: "m", API: api, Count: utf8.RuneCountInString, Meter: usage.Func(func(_ context.Context, _ string, u ai.Usage) {
			measured = append(measured, u.InChars, u.OutChars, u.EstIn-u.InChars, u.EstOut-u.OutChars) // no usage reported: estimated, in characters here
		})}).
			Exchange(ai.Prompt{Dynamic: "how?"}, []ai.Tool{tool})
		text, calls, err := e.Next(context.Background())
		if err != nil || text != "looking" || len(calls) != 2 || calls[0] != (ai.ToolCall{ID: "c1", Name: "search", Arguments: `{"query":"router"}`}) || calls[1] != (ai.ToolCall{ID: "c1", Name: "done", Key: 1}) {
			t.Fatalf("%s: %q %+v %v", api, text, calls, err)
		}
		e.Answer(calls[0], "found router.go and mux.go")
		e.Answer(calls[1], "ok")
		counted := 0
		count := func(s string) int { counted++; return len(s) }
		e.Size(count)
		e.Compact(map[int]string{0: "found router.go", 1: "okay", 9: "x"}) // the second's is no shorter, 9 no call: the first's alone, measured below
		if next, sent := e.Size(count); next-sent != 52 || e.Added() != 5 || counted != 8 {
			t.Errorf("%s: %d after the %d sent, %d pieces added, %d counted", api, next, sent, e.Added(), counted)
		}
		if text, calls, err = e.Next(context.Background()); err != nil || text != "enough" || len(calls) != 0 {
			t.Fatalf("%s, second: %q %+v %v", api, text, calls, err)
		}
		if _, _, err = e.Next(context.Background()); !errors.Is(err, ai.ErrSpent) {
			t.Errorf("%s, cut: %v", api, err)
		}
		srv.Close()
		// how? 4 and the tool's definition, d; looking, search {"query":"router"} and done 35; the results 17,
		// as compacted; enough 6. Out: the text and the arguments. Estimated alike, but the cut reply: none.
		if d := measured[0] - 4; d <= 0 || !slices.Equal(measured, []int{4 + d, 25, 0, 0, 56 + d, 6, 0, 0, 62 + d, 0, -62 - d, 0}) {
			t.Errorf("%s: measured %v", api, measured)
		}
		var first, second struct {
			Tools []struct {
				Name, Type string
				Strict     bool
				Parameters struct{ Required []string }
				Function   struct {
					Name       string
					Strict     bool
					Parameters struct{ Required []string }
				}
			}
			Messages, Input []map[string]any
		}
		if json.Unmarshal([]byte(sent[0]), &first) != nil || json.Unmarshal([]byte(sent[1]), &second) != nil || len(first.Tools) != 1 {
			t.Fatalf("%s: %s", api, sent[0])
		}
		got, want := first.Tools[0], "search true [query]"
		if api == Completions {
			got.Name, got.Strict, got.Parameters = got.Function.Name, got.Function.Strict, got.Function.Parameters
		}
		if fmt.Sprintf("%s %v %v", got.Name, got.Strict, got.Parameters.Required) != want {
			t.Errorf("%s: tool %+v", api, got)
		}
		// The turns after the question: the reply, then each result under its id.
		turns, _ := json.Marshal(append(second.Messages, second.Input...)[1:])
		for _, w := range map[string][]string{
			Completions: {`"role":"assistant"`, `"content":"looking"`, `"id":"c1"`, `{"content":"found router.go","role":"tool","tool_call_id":"c1"},{"content":"ok","role":"tool","tool_call_id":"c1"}`},
			Responses: {`{"content":"looking","role":"assistant"`, `{"arguments":"{\"query\":\"router\"}","call_id":"c1","name":"search","type":"function_call"}`,
				`{"call_id":"c1","output":"found router.go","type":"function_call_output"},{"call_id":"c1","output":"ok","type":"function_call_output"}`},
		}[api] {
			if !strings.Contains(string(turns), w) {
				t.Errorf("%s: the second request lacks %s: %s", api, w, turns)
			}
		}
	}
}
