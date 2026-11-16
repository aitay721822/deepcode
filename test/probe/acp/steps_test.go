package acpprobe

import (
	"context"
	"fmt"
	"os"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/compat"
	"deepcode/test/bench/score"
)

// unsigned is S2: an agent started with an empty login folder, what sign-in
// it offers, and what a request then gets.
func (p *prober) unsigned(line string) {
	empty := p.folder("S2-login")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		p.t.Fatal(err)
	}
	a, err := p.launch(line, []string{"CLAUDE_CONFIG_DIR=" + empty, "CODEX_HOME=" + empty}, "S2")
	if a == nil {
		p.emit("S2", map[string]any{"startError": err.Error()})
		return
	}
	defer p.stop(a)
	if err != nil {
		p.emit("S2", map[string]any{"initError": err.Error()})
		return
	}
	p.kind = kindOf(line, a)
	p.emit("S2.init", map[string]any{"kind": p.kind, "agent": a.init.AgentInfo, "auth": a.init.AuthMethods})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p.emit("S2", p.ask(ctx, a, "S2", "unsigned", "Reply in one word.", "Reply with OK."))
}

// tools is S3: whether the agent reads files or runs commands, and what of
// the user's own instructions it carries, with design 5.6's options and
// without them (plain); then the same request in every mode the agent offers.
func (p *prober) tools(ctx context.Context, a *agent) {
	const static = "You answer questions in English. You have no tools: answer from your own knowledge only."
	const run = "Read the file C:\\Windows\\win.ini and quote its first line. Then run the shell command `whoami` and give its output."
	const told = "List every instruction you were given about Context7, about MCP tools, or about which language to reply in, quoting them. If you were given none, reply NONE."
	s, ms, err := p.open(ctx, a, p.folder("S3"), static, false)
	if err != nil {
		p.emit("S3.session", map[string]any{"error": err.Error()})
		return
	}
	p.emit("S3.session", map[string]any{"newMs": ms, "response": s, "usageBeforePrompt": a.usage(s.SessionId)})
	p.close(a, s.SessionId)
	for _, plain := range []bool{false, true} {
		suffix := map[bool]string{false: "", true: "-plain"}[plain]
		p.emit("S3", p.askIn(ctx, a, "S3", "tools"+suffix, p.folder("S3"), static, run, plain))
		p.emit("S3", p.askIn(ctx, a, "S3", "settings"+suffix, p.folder("S3"), static, told, plain))
	}
	// Some of what an account carries joins a session only from its second
	// prompt (Claude's claude.ai connectors), so the question again, second.
	if s2, _, err := p.open(ctx, a, p.folder("S3"), static, false); err != nil {
		p.emit("S3", map[string]any{"case": "settings-second", "error": err.Error()})
	} else {
		p.send(ctx, a, s2.SessionId, p.text(static, "Reply with OK.", false))
		r := p.send(ctx, a, s2.SessionId, told)
		r.Step, r.Case = "S3", "settings-second"
		p.emit("S3", r)
		p.close(a, s2.SessionId)
	}
	for _, m := range modes(s) {
		id, _, err := p.open(ctx, a, p.folder("S3", "mode-"+m.name), static, false)
		if err != nil {
			p.emit("S3.mode", map[string]any{"mode": m.name, "error": err.Error()})
			continue
		}
		if err := m.set(ctx, a, id.SessionId); err != nil {
			p.emit("S3.mode", map[string]any{"mode": m.name, "error": err.Error()})
		} else {
			r := p.send(ctx, a, id.SessionId, p.text(static, run, false))
			r.Step, r.Case = "S3", "mode-"+m.name
			p.emit("S3.mode", r)
		}
		p.close(a, id.SessionId)
	}
}

// mode is a mode a session can be put in, by session/set_mode or by its
// "mode" config option.
type mode struct {
	name string
	set  func(context.Context, *agent, acp.SessionId) error
}

// modes are the modes session s offers.
func modes(s acp.NewSessionResponse) []mode {
	var out []mode
	if s.Modes != nil {
		for _, m := range s.Modes.AvailableModes {
			out = append(out, mode{string(m.Id), func(ctx context.Context, a *agent, id acp.SessionId) error {
				_, err := a.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: id, ModeId: m.Id})
				return err
			}})
		}
		return out
	}
	for _, o := range s.ConfigOptions {
		if o.Select == nil || o.Select.Id != "mode" || o.Select.Options.Ungrouped == nil {
			continue
		}
		for _, v := range *o.Select.Options.Ungrouped {
			out = append(out, mode{string(v.Value), func(ctx context.Context, a *agent, id acp.SessionId) error {
				_, err := a.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
					SessionId: id, ConfigId: o.Select.Id, Value: v.Value}})
				return err
			}})
		}
	}
	return out
}

// outline and terms are two JSON replies of the kinds deepcode asks for: a
// wiki's plan, and a question's search terms.
type (
	outline struct {
		Title string `json:"title"`
		Pages []struct {
			Title string   `json:"title" jsonschema_description:"the page's title"`
			Files []string `json:"files" jsonschema_description:"paths of the files the page explains, from the list given"`
		} `json:"pages"`
	}
	terms struct {
		Terms []string `json:"terms" jsonschema_description:"English words and code identifiers to search the code for"`
	}
)

// json is S5: 20 requests for JSON, the schema in the prompt (design 5.4),
// then how many replies were the JSON as they came, fenced, or the JSON once
// the fence is off.
func (p *prober) json(ctx context.Context, a *agent) {
	c := &compat.Client{Schema: ai.SchemaPrompt}
	verdict, plan, words := ai.Schema[score.Verdict]("verdict"), ai.Schema[outline]("outline"), ai.Schema[terms]("terms")
	type ask struct {
		kind   string
		p      ai.Prompt
		decode func(string) error
	}
	var asks []ask
	for _, ans := range []string{
		"`chi.Router` matches routes with a radix tree [tree.go:12-40]; `Mux.routeHTTP` walks it [mux.go:430-470].",
		"Based on the provided code, `Mux.routeHTTP` walks the tree [mux.go:430-470].",
		"Let me think. The middleware runs first, wait, no: `Use` appends to the stack [mux.go:100-112].",
		"Gson's `TypeAdapter` writes JSON through a `JsonWriter` [TypeAdapter.java:120-140]. How `@Since` is checked cannot be confirmed from the current code.",
		"Express builds `req.params` in `Layer.match` [lib/router/layer.js:110-150].",
		"`Router::route` は `MethodRouter` を登録します [axum/src/routing/mod.rs:160-190]。",
		"Die `OwnerController` lädt den Besitzer über `OwnerRepository.findById` [OwnerController.java:60-80].",
		"I'll answer from the sources I was given: the handler is registered in `main.go`.",
	} {
		asks = append(asks, ask{"verdict", ai.Prompt{Static: score.Rubric, Dynamic: "<answer>\n" + ans + "\n</answer>"}, check(verdict)})
	}
	files := "chi.go, mux.go, tree.go, context.go, middleware/logger.go, middleware/recoverer.go, middleware/timeout.go, middleware/compress.go, _examples/rest/main.go, chain.go"
	for n := 3; n <= 8; n++ {
		asks = append(asks, ask{"outline", ai.Prompt{Static: "You plan a wiki for a code repository: its pages and the files each explains.",
			Dynamic: fmt.Sprintf("Repository: go-chi/chi. Files: %s.\nPlan exactly %d pages; every file is on one page.", files, n)}, check(plan)})
	}
	for _, q := range []string{"How does chi match /users/{id}?", "Where does Gson cache TypeAdapters?", "How does Express run error middleware?",
		"How does axum extract JSON bodies?", "How does petclinic validate a new owner?", "How does eShop publish integration events?"} {
		asks = append(asks, ask{"terms", ai.Prompt{Static: "You turn a question about a repository into search terms for its code.", Dynamic: "Question: " + q}, check(words)})
	}
	sum := map[string]map[string]int{}
	for i, q := range asks {
		pr := formatPrompt(q.kind, c, q.p, verdict, plan, words)
		got := decoded(p.ask(ctx, a, "S5", fmt.Sprintf("%s-%d", q.kind, i+1), pr.Static, pr.Dynamic), q.decode)
		p.emit("S5", got)
		if sum[q.kind] == nil {
			sum[q.kind] = map[string]int{}
		}
		for _, k := range []string{"decodes", "fenced", "decodesUnfenced"} {
			if got[k] == true {
				sum[q.kind][k]++
			}
		}
		sum[q.kind]["asked"]++
	}
	p.emit("S5.summary", sum)
}

// formatPrompt is q as StreamJSON sends it under SchemaPrompt for its kind's format.
func formatPrompt(kind string, c *compat.Client, q ai.Prompt, verdict ai.Format[score.Verdict], plan ai.Format[outline], words ai.Format[terms]) ai.Prompt {
	switch kind {
	case "verdict":
		return verdict.Prompt(c, q)
	case "outline":
		return plan.Prompt(c, q)
	}
	return words.Prompt(c, q)
}

// check is the decode of f's JSON.
func check[T any](f ai.Format[T]) func(string) error {
	return func(s string) error { _, err := ai.Decode(f, s); return err }
}
