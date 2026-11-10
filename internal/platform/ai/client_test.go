package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// A server that repeats the key it refused gets none of it into the error:
// not whole, not where the 2048-byte cut runs through it, not past the cut.
// The status and the server's words are kept.
func TestRefusedKeyMasked(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	// The key at the start, and a second copy at offset second: before, across and past the cut.
	for _, second := range []int{100, 2048 - len(key)/2, 2050} {
		srv := serve(t, http.StatusUnauthorized, key+strings.Repeat("x", second-len(key))+key+" is not a valid key")
		_, err := (&OpenAI{BaseURL: srv.URL, APIKey: key, ModelName: "m"}).Generate(context.Background(), Prompt{})
		if err == nil || strings.Contains(err.Error(), key[:8]) || !strings.Contains(err.Error(), "m /chat/completions: 401 Unauthorized: ****") {
			t.Fatalf("key again at %d: %.120v", second, err)
		}
	}
}

// TestRefusedMessage: a refused request's error names the model, the path and
// the status, and quotes the server's words whatever their shape, on each
// endpoint: OpenAI's error object, a bare string as LM Studio sends, nothing.
func TestRefusedMessage(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":{"message":"no such model","type":"invalid_request_error"}}`, `400 Bad Request: {"error":{"message":"no such model"`},
		{`{"error":"model not loaded"}`, `400 Bad Request: {"error":"model not loaded"}`},
		{"", "400 Bad Request"},
	} {
		c := &OpenAI{BaseURL: serve(t, http.StatusBadRequest, tc.body).URL, ModelName: "m"}
		_, chat := c.Generate(context.Background(), Prompt{})
		_, embed := c.Embed(context.Background(), []string{"x"})
		c.API = Responses
		_, responses := c.Generate(context.Background(), Prompt{})
		for path, err := range map[string]error{"/chat/completions": chat, "/embeddings": embed, "/responses": responses} {
			if want := "m " + path + ": " + tc.want; err == nil || !strings.HasPrefix(err.Error(), want) || tc.body == "" && err.Error() != want {
				t.Errorf("%s, %s: %v", tc.body, path, err)
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
	if _, err := (&OpenAI{BaseURL: srv.URL + "/v1", ModelName: "m"}).Generate(context.Background(), Prompt{}); err != nil || auth.Load() != "" {
		t.Fatalf("authorization %q: %v", auth.Load(), err)
	}
}

// finished ends a Chat Completions stream as servers do: a finish reason, then [DONE].
const finished = `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"

// TestStreamEnds: an answer is whole once the server gave its finish reason,
// [DONE] or not; one without, even ended by [DONE], was cut short, and is an
// error, as is one past maxAnswer; one stopped by the output budget with
// nothing said says so.
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
		got, err := (&OpenAI{BaseURL: serve(t, 0, tc.body).URL, ModelName: "m"}).Generate(context.Background(), Prompt{})
		if tc.fails == "" && (err != nil || got != "hi") || tc.fails != "" && (err == nil || !strings.Contains(err.Error(), tc.fails)) {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
}

// TestFormat: StreamJSON asks for the schema made from a Go type, strict, on
// each API, and reads the answer into it; Generate asks for none. An answer
// the server sent that is not that JSON fails with ErrSchema, naming what the
// endpoint must support; a request the server refuses fails with its words,
// as any other, not ErrSchema; JSON cut at the output budget says so instead.
func TestFormat(t *testing.T) {
	type page struct {
		Title string   `json:"title"`
		Files []string `json:"files"`
	}
	f := Schema[struct {
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
		c := &OpenAI{BaseURL: srv.URL, ModelName: "m", API: api}
		mode.Store("")
		if v, _, err := StreamJSON(context.Background(), c, Prompt{}, f, nil); err != nil || len(v.Pages) != 1 || v.Pages[0].Files[0] != "a.go" || !strings.Contains(body.Load().(string), want) {
			t.Errorf("%s: %+v %v\n%s", api, v, err, body.Load())
		}
		if _, err := c.Generate(context.Background(), Prompt{}); err != nil || strings.Contains(body.Load().(string), "schema") {
			t.Errorf("%s, no format: %v\n%s", api, err, body.Load())
		}
		for m, says := range map[string]string{"prose": "m: the answer is not the JSON of plan", "shape": "m: the answer is not the JSON of plan", "refused": "400 Bad Request: {\"error\":{\"message\":\"response_format is not supported\"}}",
			"cut": "m: the answer reached the output budget before its JSON ended"} {
			mode.Store(m)
			_, _, err := StreamJSON(context.Background(), c, Prompt{}, f, nil)
			schema := m == "prose" || m == "shape" // an answer sent (200) that is not the schema's JSON
			if err == nil || !strings.Contains(err.Error(), says) || errors.Is(err, ErrSchema) != schema || schema && !strings.HasSuffix(err.Error(), ErrSchema.Error()) {
				t.Errorf("%s, %s: %v", api, m, err)
			}
		}
	}
}

// TestResponses: by the Responses API an answer is the output text's deltas,
// whole once the response completed or ended incomplete; a failed response,
// an error event and a stream cut short are errors, and an answer that spent
// the whole budget on reasoning says so. The request asks for nothing stored.
func TestResponses(t *testing.T) {
	ev := func(typ, rest string) string { return `data: {"type":"` + typ + `"` + rest + "}\n\n" }
	delta := ev("response.output_text.delta", `,"delta":"hi"`)
	usage := `,"usage":{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":8}}`
	for name, tc := range map[string]struct {
		body, want, fails string
	}{
		"completed":  {delta + ev("response.completed", `,"response":{"status":"completed"`+usage+`}`), "hi", ""},
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
		var got []Usage
		c := &OpenAI{BaseURL: srv.URL + "/v1", ModelName: "m", API: Responses, MaxTokens: 50, Measure: func(u Usage) { got = append(got, u) }}
		answer, err := c.Generate(context.Background(), Prompt{Static: "be brief", Dynamic: "größe"})
		srv.Close()
		if answer != tc.want && tc.fails == "" || tc.fails != "" && (err == nil || !strings.Contains(err.Error(), tc.fails)) || tc.fails == "" && err != nil {
			t.Errorf("%s: %q %v", name, answer, err)
		}
		for _, w := range []string{"/v1/responses ", `"store":false`, `"max_output_tokens":50`, `"role":"system"`, `"content":"be brief"`, `"content":"größe"`, `"stream":true`} {
			if !strings.Contains(body, w) {
				t.Errorf("%s: the request lacks %s: %s", name, w, body)
			}
		}
		if name == "completed" && (len(got) != 1 || got[0] != (Usage{InChars: 13, OutChars: 2, InTokens: 12, OutTokens: 3, CachedTokens: 8})) {
			t.Errorf("measured %+v", got)
		}
	}
}

// TestMeasure: a measured answer asks for the server's token counts and
// reports them with its characters, the reasoning not counted, the cached
// tokens the larger of OpenAI's and DeepSeek's counts; an unmeasured one asks
// for none.
func TestMeasure(t *testing.T) {
	var asked atomic.Bool
	var hit atomic.Int32 // DeepSeek's count; OpenAI's is 8
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked.Store(strings.Contains(string(body), `"include_usage":true`))
		w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"hmm, "}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"content":"héllo"},"finish_reason":"stop"}]}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_cache_hit_tokens":` + strconv.Itoa(int(hit.Load())) +
			`,"prompt_tokens_details":{"cached_tokens":8}}}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	var got []Usage
	c := &OpenAI{BaseURL: srv.URL, ModelName: "m", Measure: func(u Usage) { got = append(got, u) }}
	for _, cached := range []int32{9, 5} {
		hit.Store(cached)
		got = nil
		if answer, err := c.Generate(context.Background(), Prompt{Dynamic: "größe"}); err != nil || answer != "héllo" {
			t.Fatalf("%q %v", answer, err)
		}
		if !asked.Load() || len(got) != 1 || got[0] != (Usage{InChars: 5, OutChars: 5, InTokens: 12, OutTokens: 3, CachedTokens: int(max(cached, 8))}) {
			t.Fatalf("asked %v, measured %+v", asked.Load(), got)
		}
	}
	c.Measure = nil
	if _, err := c.Generate(context.Background(), Prompt{}); err != nil || asked.Load() {
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
		w.Header().Set("Content-Type", "application/json")
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
// caller as one from a refused request does: naming the model and path, the
// key masked, the server's words at most 2048 bytes.
func TestStreamErrorMasked(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	srv := serve(t, 0, `data: {"error":{"message":"`+key+strings.Repeat("x", 5000)+`"}}`+"\n\n")
	_, err := (&OpenAI{BaseURL: srv.URL, APIKey: key, ModelName: "m"}).Generate(context.Background(), Prompt{})
	const from = "m /chat/completions: "
	if err == nil || strings.Contains(err.Error(), key[:8]) || !strings.HasPrefix(err.Error(), from) || len(err.Error()) > len(from)+2048 {
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
	_, chat := c.Generate(context.Background(), Prompt{})
	_, embed := c.Embed(context.Background(), []string{"x"})
	c.API = Responses
	_, responses := c.Generate(context.Background(), Prompt{})
	for _, err := range []error{chat, embed, responses} {
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "from m within DEEPCODE_LLM_TIMEOUT") {
			t.Fatalf("past the timeout: %v", err)
		}
	}
}

// TestRetries: a server error is tried again, and a rate limit once the
// Retry-After it names has passed; a refused request is not, nor a rate limit
// whose Retry-After passes a minute, which fails at once.
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
		_, err := (&OpenAI{BaseURL: srv.URL, ModelName: "m"}).Generate(context.Background(), Prompt{})
		took := time.Since(start)
		srv.Close()
		if (err == nil) != (tc.calls == 2) || calls.Load() != tc.calls || took < tc.wait || took > 5*time.Second {
			t.Errorf("%s: %d requests in %v: %v", name, calls.Load(), took, err)
		}
	}
}

// TestExchange: on each API the tools go out strict, with their schema; a
// reply's calls come back in order, and the next request holds the reply and
// each result under its call's id, measured as sent; a reply cut at the
// output budget is an error, as its calls may be cut too.
func TestExchange(t *testing.T) {
	type args struct {
		Query string `json:"query"`
	}
	tool := Schema[args]("search").Tool("Search the code")
	replies := map[string][]string{
		Completions: {`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"looking","tool_calls":[` +
			`{"id":"c1","type":"function","function":{"name":"search","arguments":"{\"query\":\"router\"}"}},` +
			`{"id":"c2","type":"function","function":{"name":"done","arguments":""}}]}}]}`,
			`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"enough"}}]}`,
			`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"","tool_calls":[{"id":"c3","type":"function","function":{"name":"search","arguments":"{\"qu"}}]}}]}`},
		Responses: {`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"looking"}]},` +
			`{"type":"function_call","id":"fc1","call_id":"c1","name":"search","arguments":"{\"query\":\"router\"}"},` +
			`{"type":"function_call","id":"fc2","call_id":"c2","name":"done","arguments":""}]}`,
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
		e := (&OpenAI{BaseURL: srv.URL, ModelName: "m", API: api, Measure: func(u Usage) { measured = append(measured, u.InChars, u.OutChars) }}).
			Exchange(Prompt{Dynamic: "how?"}, []Tool{tool})
		text, calls, err := e.Next(context.Background())
		if err != nil || text != "looking" || len(calls) != 2 || calls[0] != (ToolCall{"c1", "search", `{"query":"router"}`}) || calls[1].ID != "c2" || calls[1].Name != "done" {
			t.Fatalf("%s: %q %+v %v", api, text, calls, err)
		}
		e.Answer(calls[0], "found router.go")
		e.Answer(calls[1], "ok")
		if text, calls, err = e.Next(context.Background()); err != nil || text != "enough" || len(calls) != 0 {
			t.Fatalf("%s, second: %q %+v %v", api, text, calls, err)
		}
		if _, _, err = e.Next(context.Background()); !errors.Is(err, errSpent) {
			t.Errorf("%s, cut: %v", api, err)
		}
		srv.Close()
		// how? 4; looking and {"query":"router"} 25; the results 17; enough 6.
		if want := []int{4, 25, 46, 6, 52, 0}; !slices.Equal(measured, want) {
			t.Errorf("%s: measured %v, want %v", api, measured, want)
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
			Completions: {`"role":"assistant"`, `"content":"looking"`, `"id":"c1"`, `"id":"c2"`, `{"content":"found router.go","role":"tool","tool_call_id":"c1"}`, `{"content":"ok","role":"tool","tool_call_id":"c2"}`},
			Responses: {`{"content":"looking","role":"assistant"`, `{"arguments":"{\"query\":\"router\"}","call_id":"c1","name":"search","type":"function_call"}`,
				`{"call_id":"c2","output":"ok","type":"function_call_output"}`, `{"call_id":"c1","output":"found router.go","type":"function_call_output"}`},
		}[api] {
			if !strings.Contains(string(turns), w) {
				t.Errorf("%s: the second request lacks %s: %s", api, w, turns)
			}
		}
	}
}

func TestPrompt(t *testing.T) {
	read := func(p Prompt) (s string) {
		for _, m := range p.messages() {
			s += m.Role + ":" + m.Content + " "
		}
		return s
	}
	history := append(make([]Message, 0, 4), Message{Role: "user", Content: "earlier"}) // room to grow, which After must not write into
	p := Prompt{Static: "rules", Previous: history, Dynamic: "now"}
	first, second := p.After("one", "again"), p.After("two", "once more")
	for _, c := range [][2]string{{read(p), "system:rules user:earlier user:now "}, {read(Prompt{Previous: history}) + "|" + read(Prompt{}), "user:earlier |"},
		{read(first), "system:rules user:earlier user:now assistant:one user:again "}, // second did not overwrite it
		{read(second.After("three", "last")), "system:rules user:earlier user:now assistant:two user:once more assistant:three user:last "}} {
		if c[0] != c[1] {
			t.Errorf("got %q, want %q", c[0], c[1])
		}
	}
}
