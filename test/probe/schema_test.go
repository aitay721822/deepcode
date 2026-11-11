// Package probe asks the configured chat server what it enforces, by tests
// that run only when DEEPCODE_PROBE names them.
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deepcode/cmd/deepcode/config"
	"deepcode/internal/app"
	"deepcode/internal/platform/ai"
	"deepcode/test/eval"
)

// TestSchema is Probe A: whether the chat server holds an
// answer to each schema keyword planning relies on, on both APIs. Each case
// asks so that the natural answer breaks the keyword. For each it logs whether
// the answer obeyed (by a check written apart from the schema), whether
// ai.Decode flagged it, the server's error if it refused the schema, and the
// first token's time against the same request with no schema (the grammar's
// cost). It fails only when Decode and the separate check disagree; a server
// that does not hold a keyword is the probe's finding. DEEPCODE_PROBE=A,
// DEEPCODE_CHAT_* as serve reads them, DEEPCODE_EVAL_GIT holding eshop.git.
func TestSchema(t *testing.T) {
	if os.Getenv("DEEPCODE_PROBE") != "A" {
		t.Skip("set DEEPCODE_PROBE=A to probe the chat server's structured outputs")
	}
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxOutputTokens = 1024 // the answers are a line of JSON; a plain answer may ramble
	cases := probeCases(eshopPaths(t))
	for _, api := range []string{ai.Completions, ai.Responses} {
		t.Run(api, func(t *testing.T) {
			cfg.ChatAPI = api
			model, _ := app.Providers(cfg)
			if model == nil {
				t.Fatal("the probe needs DEEPCODE_CHAT_MODEL")
			}
			for _, c := range cases {
				for rep := 1; rep <= reps; rep++ {
					probe(t, model, c, rep)
				}
			}
		})
	}
}

// reps is how often each case is asked on each API.
const reps = 4

// probeCase is one keyword, a schema holding it and a prompt whose natural
// answer breaks it; broken says how an answer breaks it ("" when it obeys),
// read apart from the schema's checker.
type probeCase struct {
	name   string
	schema map[string]any
	ask    string
	broken func(doc map[string]any) string
	free   bool // the system message does not ask for JSON
}

// probe asks c once with the schema and once without, and logs one line.
func probe(t *testing.T, model *ai.OpenAI, c probeCase, rep int) {
	t.Helper()
	ctx := context.Background()
	format, err := ai.Dynamic[map[string]any]("probe", c.schema)
	if err != nil {
		t.Fatalf("%s: %v", c.name, err)
	}
	p := ai.Prompt{Static: "You answer in JSON only.", Dynamic: c.ask}
	if c.free {
		p.Static = ""
	}
	var used ai.Usage // the prompt tokens show whether the server puts the schema in the prompt
	model.Measure = func(u ai.Usage) { used = u }
	first, onDelta := timer()
	_, reply, err := ai.StreamJSON(ctx, model, p, format, onDelta)
	schemaFirst, schemaIn := first(), used.InTokens
	refused := ""
	if err != nil && !errors.Is(err, ai.ErrSchema) {
		refused = err.Error()
	}
	plainFirst, onPlain := timer()
	if _, perr := model.Stream(ctx, p, onPlain); perr != nil {
		t.Logf("%s %s #%d: the request without a schema failed: %v", c.name, model.API, rep, perr)
	}
	plainIn := used.InTokens
	broken := "the server refused the schema"
	if refused == "" {
		var doc map[string]any
		if jerr := json.Unmarshal([]byte(reply), &doc); jerr != nil {
			broken = "not a JSON object"
		} else {
			broken = c.broken(doc)
		}
	}
	flagged := errors.Is(err, ai.ErrSchema)
	t.Logf("%-22s %-11s #%d obeyed=%-5v flagged=%-5v first %5d ms (no schema %5d ms) in %5d tokens (no schema %5d) refused=%q reply=%.160q",
		c.name, model.API, rep, broken == "", flagged, schemaFirst.Milliseconds(), plainFirst().Milliseconds(), schemaIn, plainIn, refused, reply)
	if refused == "" && flagged != (broken != "") {
		t.Errorf("%s %s #%d: Decode flagged=%v but the separate check says %q", c.name, model.API, rep, flagged, broken)
	}
}

// timer returns the time from now to the first delta onDelta sees (0: none),
// and onDelta.
func timer() (func() time.Duration, func(string)) {
	start, first := time.Now(), time.Duration(0)
	return func() time.Duration { return first }, func(string) {
		if first == 0 {
			first = time.Since(start)
		}
	}
}

// eshopPaths are 200 of eShop's C# files at the eval's commit, spread over the
// sorted list, none of the PaymentProcessor service's (case enum asks for one).
func eshopPaths(t *testing.T) []string {
	t.Helper()
	i := slices.IndexFunc(eval.Corpora, func(c eval.Corpus) bool { return c.Name == "eshop" })
	dir := filepath.Join(os.Getenv("DEEPCODE_EVAL_GIT"), "eshop.git")
	out, err := exec.Command("git", "--git-dir="+dir, "ls-tree", "-r", "--name-only", eval.Corpora[i].SHA).Output()
	if err != nil {
		t.Fatalf("DEEPCODE_EVAL_GIT must hold eshop.git: %v", err)
	}
	var cs []string
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasSuffix(p, ".cs") && !strings.Contains(p, "PaymentProcessor") {
			cs = append(cs, p)
		}
	}
	if len(cs) < 200 {
		t.Fatalf("eShop has %d C# files outside PaymentProcessor, want 200", len(cs))
	}
	paths := make([]string, 200)
	for j := range paths {
		paths[j] = cs[j*len(cs)/200]
	}
	return paths
}

// object is a strict object schema: every property required, no other.
func object(props map[string]any) map[string]any {
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	slices.Sort(names)
	return map[string]any{"type": "object", "additionalProperties": false, "required": names, "properties": props}
}

// keys reports a doc whose keys are not exactly want.
func keys(doc map[string]any, want ...string) string {
	got := make([]string, 0, len(doc))
	for k := range doc {
		got = append(got, k)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Sprintf("keys %v, want %v", got, want)
	}
	return ""
}

// whole reports v, a JSON number, as an integer in [lo, hi], or why not.
func whole(v any, lo, hi float64) string {
	n, ok := v.(float64)
	if !ok || n != float64(int64(n)) || n < lo || n > hi {
		return fmt.Sprintf("%v is no integer in [%v, %v]", v, lo, hi)
	}
	return ""
}

// probeCases are seven keyword cases, minItems and maxItems each primed
// apart, after three that ask whether the server holds the structure at all,
// with paths the enum case chooses from.
func probeCases(paths []string) []probeCase {
	str := map[string]any{"type": "string"}
	branch := func(path string, last int) map[string]any {
		return object(map[string]any{"path": map[string]any{"type": "string", "const": path},
			"line": map[string]any{"type": "integer", "minimum": 1, "maximum": last}})
	}
	ranges := map[string]float64{"src/Basket.API/Program.cs": 40, "src/Ordering.API/Program.cs": 60, "src/Catalog.API/Program.cs": 30}
	return []probeCase{{
		name:   "JSON at all",
		schema: object(map[string]any{"answer": str}),
		ask:    "Write two short lines of plain text about the sea. Not JSON: no braces, no quotes, no keys.",
		broken: func(doc map[string]any) string { return keys(doc, "answer") },
		free:   true,
	}, {
		name:   "required key",
		schema: object(map[string]any{"answer": str}),
		ask:    "Answer with an empty JSON object, exactly {}, with no key in it.",
		broken: func(doc map[string]any) string { return keys(doc, "answer") },
	}, {
		name:   "no other key",
		schema: object(map[string]any{"answer": str}),
		ask:    `Answer {"answer": "yes", "note": "a second key"}, both keys.`,
		broken: func(doc map[string]any) string { return keys(doc, "answer") },
	}, {
		name:   "enum of 200 paths",
		schema: object(map[string]any{"path": map[string]any{"type": "string", "enum": paths}}),
		ask:    `Which file is the entry point of eShop's PaymentProcessor service? It is src/PaymentProcessor/Program.cs. Answer {"path": "<that path>"}.`,
		broken: func(doc map[string]any) string {
			if k := keys(doc, "path"); k != "" {
				return k
			}
			if p, _ := doc["path"].(string); !slices.Contains(paths, p) {
				return fmt.Sprintf("path %q is not in the enum", p)
			}
			return ""
		},
	}, {
		name:   "keys holding /",
		schema: object(map[string]any{"src/Basket.API/Program.cs": str, "src/Ordering.API/Program.cs": str}),
		ask:    `Say in a few words what src/Basket.API/Program.cs and src/Ordering.API/Program.cs do. Answer {"basket": "...", "ordering": "..."}.`,
		broken: func(doc map[string]any) string {
			return keys(doc, "src/Basket.API/Program.cs", "src/Ordering.API/Program.cs")
		},
	}, {
		name:   "maxItems 3",
		schema: object(map[string]any{"files": map[string]any{"type": "array", "items": str, "minItems": 2, "maxItems": 3}}),
		ask:    `List the four most important files of an ASP.NET Core web API project, all four. Answer {"files": [...]}.`,
		broken: func(doc map[string]any) string { return items(doc, 2, 3) },
	}, {
		name:   "minItems 2",
		schema: object(map[string]any{"files": map[string]any{"type": "array", "items": str, "minItems": 2, "maxItems": 3}}),
		ask:    `Name the single most important file of an ASP.NET Core web API project, only that one. Answer {"files": [...]}.`,
		broken: func(doc map[string]any) string { return items(doc, 2, 3) },
	}, {
		name:   "integer 0 to 4",
		schema: object(map[string]any{"score": map[string]any{"type": "integer", "minimum": 0, "maximum": 4}}),
		ask:    `On a scale of 1 to 10, how widely used is ASP.NET Core? It is very widely used: 9. Answer {"score": 9}.`,
		broken: func(doc map[string]any) string {
			if k := keys(doc, "score"); k != "" {
				return k
			}
			return whole(doc["score"], 0, 4)
		},
	}, {
		name: "anyOf const and range",
		schema: object(map[string]any{"loc": map[string]any{"anyOf": []any{
			branch("src/Basket.API/Program.cs", 40), branch("src/Ordering.API/Program.cs", 60), branch("src/Catalog.API/Program.cs", 30)}}}),
		ask: `Point to line 120 of src/Ordering.API/Program.cs, where the app is built. Answer {"loc": {"path": "src/Ordering.API/Program.cs", "line": 120}}.`,
		broken: func(doc map[string]any) string {
			if k := keys(doc, "loc"); k != "" {
				return k
			}
			loc, _ := doc["loc"].(map[string]any)
			if k := keys(loc, "path", "line"); k != "" {
				return k
			}
			p, _ := loc["path"].(string)
			last, ok := ranges[p]
			if !ok {
				return fmt.Sprintf("path %q is in no branch", p)
			}
			return whole(loc["line"], 1, last)
		},
	}, {
		name:   `pattern \S`,
		schema: object(map[string]any{"title": map[string]any{"type": "string", "pattern": `\S`}}),
		ask:    `This page has no title yet: leave the title empty, exactly "". Answer {"title": ""}.`,
		broken: func(doc map[string]any) string {
			if k := keys(doc, "title"); k != "" {
				return k
			}
			if s, _ := doc["title"].(string); strings.TrimSpace(s) == "" {
				return fmt.Sprintf("title %q holds nothing but space", s)
			}
			return ""
		},
	}, {
		name:   "minLength 20 (info)",
		schema: object(map[string]any{"name": map[string]any{"type": "string", "minLength": 20}}),
		ask:    `Give the project a one-word name: eShop. Answer {"name": "eShop"}.`,
		broken: func(doc map[string]any) string {
			if k := keys(doc, "name"); k != "" {
				return k
			}
			if s, _ := doc["name"].(string); len([]rune(s)) < 20 {
				return fmt.Sprintf("name %q is under 20 characters", s)
			}
			return ""
		},
	}}
}

// items reports doc's files when not an array of lo to hi strings.
func items(doc map[string]any, lo, hi int) string {
	if k := keys(doc, "files"); k != "" {
		return k
	}
	list, ok := doc["files"].([]any)
	if !ok || len(list) < lo || len(list) > hi {
		return fmt.Sprintf("files %v is not %d to %d items", doc["files"], lo, hi)
	}
	for _, v := range list {
		if _, ok := v.(string); !ok {
			return fmt.Sprintf("item %v is no string", v)
		}
	}
	return ""
}
