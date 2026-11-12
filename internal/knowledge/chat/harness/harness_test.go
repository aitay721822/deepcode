package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"deepcode/internal/platform/ai"
)

// model is a Chat Completions server answering each request with the next of
// answers, an HTTP status and a body (a streamed answer's whole stream), the
// last again once they run out; it keeps every request's body.
func model(t *testing.T, answers ...string) (*ai.OpenAI, func() []string) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		answer := answers[min(len(bodies), len(answers)-1)]
		bodies = append(bodies, string(b))
		mu.Unlock()
		status, body, _ := strings.Cut(answer, " ")
		w.Header().Set("Content-Type", map[bool]string{true: "text/event-stream", false: "application/json"}[strings.HasPrefix(body, "data:")])
		if status != "200" {
			w.WriteHeader(map[string]int{"400": http.StatusBadRequest}[status])
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &ai.OpenAI{BaseURL: srv.URL, ModelName: "m", MaxTokens: 100}, func() []string { mu.Lock(); defer mu.Unlock(); return bodies }
}

// reply is a whole reply calling search with the given call id, or ("") none,
// and, given, the prompt tokens its usage reports.
func reply(id string, prompt ...int) string {
	msg := map[string]any{"role": "assistant", "content": ""}
	if id != "" {
		msg["tool_calls"] = []any{map[string]any{"id": id, "type": "function", "function": map[string]string{"name": "search", "arguments": `{}`}}}
	}
	r := map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": msg}}}
	if len(prompt) > 0 {
		r["usage"] = map[string]int{"prompt_tokens": prompt[0], "completion_tokens": 1, "total_tokens": prompt[0] + 1}
	}
	b, _ := json.Marshal(r)
	return "200 " + string(b)
}

// over is llama.cpp's server refusing a request of prompt tokens, its context ctx.
func over(prompt, ctx int) string {
	return fmt.Sprintf(`400 {"error":{"code":400,"message":"request (%d tokens) exceeds the available context size (%d tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":%[1]d,"n_ctx":%[2]d}}`, prompt, ctx)
}

// TestThreshold: a request may take CompactRatio of the context (0.8 unset),
// never past the context less the answer's room (MaxTokens, else 8192); none
// is known with no context, and Room is then unbounded. A prompt takes its
// messages' tokens, and perMessage each.
func TestThreshold(t *testing.T) {
	for _, tc := range []struct {
		window, room int
		ratio        float64
		want         int
	}{{0, 0, 0, 0}, {524288, 0, 0, 419430}, {16000, 0, 0, 7808}, {8192, 2048, 0.8, 6144}, {8192, 1024, 0.5, 4096}} {
		f := New(&ai.OpenAI{Window: tc.window, MaxTokens: tc.room, CompactRatio: tc.ratio}, o200k)
		if got := f.Threshold(); got != tc.want || tc.window == 0 && f.Room() != math.MaxInt || tc.window > 0 && f.Room() != tc.want {
			t.Errorf("%+v: threshold %d, room %d", tc, got, f.Room())
		}
	}
	if n := New(&ai.OpenAI{}, o200k).Prompt(ai.Prompt{Static: "hello", Dynamic: "world"}); n != 2+2*perMessage {
		t.Errorf("a prompt of two messages: %d tokens", n)
	}
}

// TestSummaryRoom: a summary's request is sized as it is sent
// (ai.Format.Prompt), its schema in it under ai.SchemaPrompt: a context that
// leaves its one message 60 tokens asks the model, a token less does not.
func TestSummaryRoom(t *testing.T) {
	for _, schema := range []string{ai.SchemaServer, ai.SchemaPrompt} {
		need := o200k.Count(summedFormat.Prompt(&ai.OpenAI{Schema: schema}, ai.Prompt{Static: summaryPrompt}).Static) + 300 + 60
		for room, asked := range map[int]bool{need: true, need - 1: false} {
			m, sent := model(t, `400 {"error":{"message":"no"}}`)
			m.Schema, m.Window, m.CompactRatio = schema, room+m.MaxTokens, 1
			if _, err := New(m, o200k).summarize(context.Background(), "", []ai.Message{{Role: "user", Content: "q"}}); err == nil || len(sent()) > 0 != asked {
				t.Errorf("%s, a room of %d: %v, %d asked", schema, room, err, len(sent()))
			}
		}
	}
}

// TestShare: at 524,288 tokens every part gets the fixed share it wants; at
// 16,000 the background goes first, the conversation keeps summaryRoom for its
// summary, the evidence takes the rest, never less than minEvidence; with no
// room for the least evidence ErrSmall names a context, the least that has it,
// exactly, whatever the floors. A deep answer's evidence (rest) also takes
// what the conversation and the background leave whole: never less than
// without, so at 16,000 and 32,000 it is the same.
func TestShare(t *testing.T) {
	want, m := Shares{Evidence: 64000, History: 33000, Background: 6300}, &ai.OpenAI{Window: 524288}
	f := New(m, o200k)
	if got, err := f.Share(5000, want, false); err != nil || got != want {
		t.Errorf("524K: %+v %v", got, err)
	}
	if got, err := f.Share(5000, want, true); err != nil || got != (Shares{Evidence: 419430 - 5000 - 33000 - 6300, History: 33000, Background: 6300}) {
		t.Errorf("524K, the rest: %+v %v", got, err)
	}
	m.Window = 16000 // 7808 tokens
	for _, rest := range []bool{false, true} {
		if got, err := f.Share(1500, want, rest); err != nil || got != (Shares{Evidence: 5708, History: 600}) {
			t.Errorf("16K, rest %v: %+v %v", rest, got, err)
		}
	}
	if got, err := f.Share(5000, want, false); err != nil || got != (Shares{Evidence: minEvidence, History: 308}) {
		t.Errorf("16K, the least evidence: %+v %v", got, err)
	}
	m.Window = 32000 // 23,808 tokens: a conversation of 16,500 and a page of 6,000 leave the evidence less than its share
	if got, err := f.Share(1500, Shares{Evidence: 64000, History: 16500, Background: 6000}, true); err != nil || got != (Shares{Evidence: 21708, History: 600}) {
		t.Errorf("32K, the rest: %+v %v", got, err)
	}
	m.Window = 12000
	var small *ErrSmall
	if _, err := f.Share(1500, want, true); !errors.As(err, &small) || small.Need != 12192 || !strings.Contains(err.Error(), "DEEPCODE_CHAT_CONTEXT 都要至少 12192 tokens") {
		t.Fatalf("12K: %v", err)
	}
	for window, fits := range map[int]bool{12192: true, 12191: false} {
		if m.Window = window; fits != (func() bool { _, err := f.Share(1500, want, false); return err == nil })() {
			t.Errorf("%d tokens: fits %v", window, !fits)
		}
	}
	m.CompactRatio, f.slack = 0.7, 33 // the ratio bound, its floor, and slack a refusal taught
	for fixed := 40000; fixed < 40200; fixed++ {
		m.Window = 9000
		_, err := f.Share(fixed, want, false)
		if !errors.As(err, &small) {
			t.Fatal(err)
		}
		m.Window = small.Need
		_, fits := f.Share(fixed, want, false)
		m.Window = small.Need - 1
		if _, short := f.Share(fixed, want, false); fits != nil || short == nil {
			t.Fatalf("%d fixed: %d tokens fit %v, one fewer %v", fixed, small.Need, fits == nil, short == nil)
		}
	}
}

// TestUnset: with no context known (chat_context unset, none reported), each
// share is what its part wants, the fixed one; no result is masked however
// large the request; and the conversation comes back whole, nothing loaded and
// no summary asked for.
func TestUnset(t *testing.T) {
	m, sent := model(t, reply("c1"), reply(""))
	f, want := New(m, o200k), Shares{Evidence: 64000, History: 1 << 20, Background: 6000}
	for _, rest := range []bool{false, true} {
		if got, err := f.Share(1<<30, want, rest); err != nil || got != want {
			t.Errorf("shares %+v %v", got, err)
		}
	}
	ex := m.Exchange(ai.Prompt{Dynamic: "how?"}, nil)
	_, calls, err := f.Next(context.Background(), ex, nil)
	if err == nil {
		ex.Answer(calls[0], strings.Repeat("x", 1<<20))
		_, _, err = f.Next(context.Background(), ex, func() map[int]string { t.Error("masks asked for"); return nil })
	}
	turns := []Turn{{1, ai.Message{Role: "user", Content: strings.Repeat("q", 5000)}}, {2, ai.Message{Role: "assistant", Content: "a"}}}
	r, rerr := f.Recall(context.Background(), turns, f.History(turns), func(context.Context) (Summary, error) { t.Error("a summary loaded"); return Summary{}, nil })
	if err != nil || rerr != nil || f.Masked != 0 || len(sent()) != 2 || len(r.Kept) != 2 || r.Kept[0] != turns[0].Message || r.Lead != "" || r.Fresh != nil || r.Summed != 0 {
		t.Errorf("%v %v: masked %d, %d requests, %+v", err, rerr, f.Masked, len(sent()), r)
	}
}

// TestLearn: a refusal as too long that gives the counts teaches the context,
// for the model while it runs, and the slack, the tokens the server counted
// past ours, the most of any refusal; one without them, or another error,
// teaches nothing. A context set smaller than the one reported takes over.
func TestLearn(t *testing.T) {
	m := &ai.OpenAI{Window: 16000, MaxTokens: 1000}
	f := New(m, o200k)
	if f.Learn(errors.New("502"), 900) || f.Learn(&ai.ErrContext{}, 900) || f.slack != 0 {
		t.Fatal("learned from no counts")
	}
	if !f.Learn(fmt.Errorf("asked: %w", &ai.ErrContext{Prompt: 9000, Context: 8192}), 8500) || m.Context() != 8192 || f.slack != 500 || f.Room() != 6553-500 || f.Over(6553) { // a gathering's count has no slack
		t.Fatalf("context %d, slack %d", m.Context(), f.slack)
	}
	if f.Learn(&ai.ErrContext{Prompt: 9000, Context: 8192}, 8900); f.slack != 500 {
		t.Errorf("slack %d after a closer count", f.slack)
	}
	if m.Window = 4096; m.Context() != 4096 {
		t.Errorf("a smaller window set: %d", m.Context())
	}
}

// TestNext: a request that would pass the threshold has the results masks
// gives masked before it is sent, the last reply's kept; one that still would
// is not sent (ErrFull). A refusal as too long with the counts teaches the
// context and the template's tokens, as the server counted the request (no
// slack), the request masked and sent once more; a second refusal, or one
// without the counts, is ErrFull. A reply's usage anchors the template's tokens.
func TestNext(t *testing.T) {
	long, short := strings.Repeat("route ", 600), "search found a.go:1-9 Find"
	start := func(answers ...string) (*Fit, *ai.Exchange, func() []string) {
		m, sent := model(t, append([]string{reply("c1")}, answers...)...)
		f, ex := New(m, o200k), m.Exchange(ai.Prompt{Dynamic: strings.Repeat("how does it route? ", 50)}, nil)
		_, calls, err := f.Next(context.Background(), ex, nil)
		if err != nil || len(calls) != 1 {
			t.Fatal(calls, err)
		}
		ex.Answer(calls[0], long)
		return f, ex, sent
	}
	masks := func() map[int]string { return map[int]string{0: short} }
	// The request whole and masked: no reply measured the template, so toolsNote, and perMessage a piece added.
	f, ex, _ := start()
	whole := f.size(ex)
	masked := whole - o200k.Message(long) + o200k.Message(short)
	window := func(threshold int) int { return int(math.Ceil(float64(threshold) / 0.8)) } // past 500 tokens, MaxTokens 100 binds no threshold
	if f.model.Window = window(masked); f.Threshold() < masked || f.Threshold() >= whole || whole != o200k.Message(strings.Repeat("how does it route? ", 50))+o200k.Message("search{}")+o200k.Message(long)+2*perMessage+toolsNote {
		t.Fatalf("whole %d, masked %d, threshold %d", whole, masked, f.Threshold())
	}
	f, ex, sent := start(reply(""))
	f.model.Window = window(masked)
	if _, _, err := f.Next(context.Background(), ex, masks); err != nil || f.Masked != 1 || len(sent()) != 2 || !strings.Contains(sent()[1], short) || strings.Contains(sent()[1], long) {
		t.Errorf("masked: %v, %d masked, %d sent", err, f.Masked, len(sent()))
	}
	f, ex, sent = start(reply(""))
	if f.model.Window = window(masked) - 2; f.Threshold() >= masked {
		t.Fatalf("threshold %d", f.Threshold())
	}
	if _, _, err := f.Next(context.Background(), ex, masks); !errors.Is(err, ErrFull) || len(sent()) != 1 {
		t.Errorf("too large masked: %v, %d sent", err, len(sent()))
	}
	f, ex, sent = start(over(whole+5, window(masked+5)), reply("")) // the server counted 5 more: the template is toolsNote and 5
	if _, _, err := f.Next(context.Background(), ex, masks); err != nil || f.Masked != 1 || f.model.Context() != window(masked+5) || f.overhead != toolsNote+5 || f.slack != 0 || len(sent()) != 3 || !strings.Contains(sent()[2], short) {
		t.Errorf("learned: %v, %d masked, context %d, template %d, slack %d, %d sent", err, f.Masked, f.model.Context(), f.overhead, f.slack, len(sent()))
	}
	f, ex, sent = start(over(whole+5, window(masked+5)), over(masked+500, window(masked+5)))
	if _, _, err := f.Next(context.Background(), ex, masks); !errors.Is(err, ErrFull) || !errors.As(err, new(*ai.ErrContext)) || len(sent()) != 3 {
		t.Errorf("refused twice: %v, %d sent", err, len(sent()))
	}
	f, ex, sent = start(`400 {"error":{"message":"too long","code":"context_length_exceeded"}}`)
	if _, _, err := f.Next(context.Background(), ex, masks); !errors.Is(err, ErrFull) || f.Masked != 0 || len(sent()) != 2 {
		t.Errorf("no counts: %v, %d sent", err, len(sent()))
	}
	asked := o200k.Count(strings.Repeat("q", 1000)) // and the template's 52, as the server counts
	m, _ := model(t, reply("c1", asked+52))
	m.Window = 524288
	g, q := New(m, o200k), m.Exchange(ai.Prompt{Dynamic: strings.Repeat("q", 1000)}, nil)
	if size := g.size(q); size != asked+perMessage+toolsNote {
		t.Errorf("before a reply: %d", size)
	}
	_, calls, err := g.Next(context.Background(), q, nil)
	if q.Answer(calls[0], "found it"); err != nil || g.size(q) != asked+52+o200k.Count("search{}")+o200k.Count("found it")+2*perMessage {
		t.Errorf("anchored: %v, %d", err, g.size(q))
	}
}

// TestQwenOnO200k: the o200k fallback counting text a Qwen model counts some
// 10% higher, at a 16,000-token context, where the threshold is the context
// less the answer's room and so leaves no slack: a request the fallback
// counts within the threshold overflows, once, a server that refuses a request
// past it; the resend, fitted by the server's count of it, fits. No text
// counted is longer than the request's longest piece.
func TestQwenOnO200k(t *testing.T) {
	var mu sync.Mutex
	var asked []int // the server's count of each request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content any } }
		json.NewDecoder(r.Body).Decode(&body)
		n := 0
		for _, m := range body.Messages {
			if s, ok := m.Content.(string); ok {
				n += o200k.Count(s)
			}
		}
		n = n*11/10 + 52 // Qwen's tokens, and its template's
		mu.Lock()
		asked = append(asked, n)
		first := len(asked) == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n > 16000-ai.DefaultOutput { // with the answer's room, past the context
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(over(n, 16000)[4:]))
			return
		}
		w.Write([]byte(reply(map[bool]string{true: "c1"}[first], n)[4:]))
	}))
	defer srv.Close()
	m, longest := &ai.OpenAI{BaseURL: srv.URL, ModelName: "m", Window: 16000}, 0
	f, ex := New(m, Counting("o200k, the longest text kept", func(s string) int { longest = max(longest, len(s)); return o200k.Count(s) })),
		m.Exchange(ai.Prompt{Dynamic: strings.Repeat("How is a request routed? ", 20)}, nil)
	_, calls, err := f.Next(context.Background(), ex, nil)
	if err != nil || len(calls) != 1 {
		t.Fatal(calls, err)
	}
	result := strings.Repeat(" word", 7200)
	if ex.Answer(calls[0], result); f.size(ex) > f.Threshold() {
		t.Fatalf("the fallback counts %d of %d", f.size(ex), f.Threshold())
	}
	_, _, err = f.Next(context.Background(), ex, func() map[int]string { return map[int]string{0: "search found router.go:1-90"} })
	if err != nil || f.Masked != 1 || len(asked) != 3 || asked[1] <= 16000-ai.DefaultOutput || asked[2] > 16000-ai.DefaultOutput || f.slack != 0 || longest > len(result) {
		t.Errorf("%v: %d masked, the server counted %v, slack %d, %d bytes counted at once", err, f.Masked, asked, f.slack, longest)
	}
}

// stream is the model answering content as a stream.
func stream(content string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
	return "200 data: " + string(b) + "\n\ndata: [DONE]\n\n"
}

// streamed is the model's summary as a stream: topics, names and no open question.
var streamed = stream(`{"topics":["routing"],"names":["findRoute","src/tree.go"],"open":[]}`)

// TestRecall: turns past the share keep the newest that fit, and a summary of
// those before them heads the first kept, the user's, or, none kept, leads the
// new message: the model writes it, asked in its schema, names first, from the
// turns it has not summed up yet and the stored summary, which it returns to
// store, at most maxSummary bytes; one stored that covers them is reused
// without asking. What comes back takes no more than the share, the summary cut
// to fit. A summary that fails leaves the turns that fit, and the error.
func TestRecall(t *testing.T) {
	turns := []Turn{{1, ai.Message{Role: "user", Content: "A " + strings.Repeat("alpha bravo ", 400)}}, {2, ai.Message{Role: "assistant", Content: "B " + strings.Repeat("bravo charlie ", 400)}},
		{3, ai.Message{Role: "user", Content: "and Mount?"}}, {4, ai.Message{Role: "assistant", Content: "Mount attaches a sub-router."}}}
	stored := func(s Summary) func(context.Context) (Summary, error) {
		return func(context.Context) (Summary, error) { return s, nil }
	}
	fits := func(r Recalled, share int) bool {
		var kept []Turn
		for _, m := range r.Kept {
			kept = append(kept, Turn{Message: m})
		}
		return New(&ai.OpenAI{}, o200k).History(kept)+o200k.Count(r.Lead) <= share
	}
	f := New(&ai.OpenAI{}, o200k)
	share := f.History(turns[2:]) + summaryRoom // the last two turns and a summary
	for name, tc := range map[string]struct {
		stored        Summary
		answer, says  string // the model's, and what the summary says
		asked, absent string // in the summary's request, and not
	}{
		"new":     {Summary{}, streamed, "Named: findRoute, src/tree.go. Topics: routing. Open: none.", `"name":"conversation_summary"`, "and Mount?"},
		"rolled":  {Summary{1, "prior"}, streamed, "Named: findRoute, src/tree.go.", `The summary so far:\nprior`, "A alpha"},
		"reused":  {Summary{2, "prior"}, "", "prior", "", ""},
		"failing": {Summary{}, `400 {"error":{"message":"no"}}`, "", "", ""},
	} {
		m, sent := model(t, tc.answer)
		r, err := New(m, o200k).Recall(context.Background(), turns, share, stored(tc.stored))
		ok := len(r.Kept) == 2 && strings.HasSuffix(r.Kept[0].Content, "and Mount?") && r.Kept[1] == turns[3].Message && r.Lead == "" && fits(r, share)
		switch name {
		case "reused":
			ok = ok && err == nil && r.Fresh == nil && r.Summed == 2 && len(sent()) == 0
		case "failing":
			ok = ok && err != nil && r.Fresh == nil && r.Summed == 0 && r.Kept[0] == turns[2].Message
		default:
			ok = ok && err == nil && r.Fresh != nil && r.Fresh.Through == 2 && r.Summed == 2 && len(sent()) == 1 &&
				strings.Contains(sent()[0], tc.asked) && !strings.Contains(sent()[0], tc.absent) && strings.Contains(sent()[0], "B bravo charlie bravo")
		}
		if !ok || tc.says != "" && !strings.HasPrefix(r.Kept[0].Content, "Earlier in this conversation, summed up: "+map[bool]string{true: "prior", false: tc.says}[name == "reused"]) {
			t.Errorf("%s: %+v, %v; asked %q", name, r, err, sent())
		}
	}
	// None kept: the summary leads the new message, so that no two user messages
	// follow each other; in a share too small for it, cut.
	for share, lead := range map[int]string{summaryRoom + f.turn(turns[3]) - 1: "Earlier in this conversation, summed up: Named: findRoute, src/tree.go. Topics: routing. Open: none.\n\n", 20: "Earlier in this conversation, summed up: Named…\n\n"} {
		m, _ := model(t, streamed)
		if r, err := New(m, o200k).Recall(context.Background(), turns, share, stored(Summary{})); err != nil || len(r.Kept) != 0 || r.Summed != 4 || r.Lead != lead || !fits(r, share) {
			t.Errorf("none kept in %d: %+v %v", share, r, err)
		}
	}
	// The first turn kept the assistant's: the summary is a user message of its own before it.
	m, _ := model(t, streamed)
	if r, err := New(m, o200k).Recall(context.Background(), turns, summaryRoom+f.turn(turns[3]), stored(Summary{})); err != nil || len(r.Kept) != 2 || r.Kept[0].Role != "user" || r.Kept[1] != turns[3].Message ||
		!strings.HasPrefix(r.Kept[0].Content, "Earlier in this conversation, summed up: Named: findRoute") || r.Lead != "" || r.Summed != 3 || r.Fresh.Through != 3 {
		t.Errorf("assistant first: %+v %v", r, err)
	}
	m, _ = model(t, stream(`{"topics":["t"],"names":["`+strings.Repeat("n", 5000)+`"],"open":[]}`))
	if r, err := New(m, o200k).Recall(context.Background(), turns, share, stored(Summary{})); err != nil || len(r.Fresh.Text) > maxSummary || !strings.HasPrefix(r.Fresh.Text, "Named: nnn") {
		t.Errorf("a long summary stored: %d bytes, %v", len(r.Fresh.Text), err)
	}
	if r, err := New(&ai.OpenAI{}, o200k).Recall(context.Background(), turns, 0, stored(Summary{})); r.Kept != nil || r.Lead != "" || err != nil {
		t.Errorf("no room: %+v %v", r, err)
	}
}
