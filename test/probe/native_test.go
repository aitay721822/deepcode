package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"deepcode/cmd/deepcode/config"
	"deepcode/internal/platform/ai"
)

// TestNative is Probe A2: whether the chat server constrains decoding when
// asked the way llama.cpp's server is (a top-level json_schema or grammar, or
// response_format json_object with a schema), on Probe A's two sharpest cases,
// by Chat Completions, each request built with the SDK and its documented
// option.WithJSONSet. It logs the server's /props first. Each line says whether
// the answer obeyed (Probe A's separate check), whether ai.Decode flagged it,
// the prompt tokens (they grow when the schema goes into the prompt) and the
// first token's time. DEEPCODE_PROBE=A2, DEEPCODE_CHAT_* as serve reads them,
// DEEPCODE_EVAL_GIT holding eshop.git.
func TestNative(t *testing.T) {
	if os.Getenv("DEEPCODE_PROBE") != "A2" {
		t.Skip("set DEEPCODE_PROBE=A2 to probe the chat server's native constraints")
	}
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChatModel == "" {
		t.Fatal("the probe needs DEEPCODE_CHAT_MODEL")
	}
	props(t, strings.TrimSuffix(cfg.ChatURL, "/v1")+"/props", cfg.ChatKey)
	cases := probeCases(eshopPaths(t))
	pick := func(name string) probeCase {
		return cases[slices.IndexFunc(cases, func(c probeCase) bool { return c.name == name })]
	}
	required, enum := pick("required key"), pick("enum of 200 paths")
	// A GBNF of the required-key case's schema: one object, its one key.
	grammar := `root ::= "{" ws "\"answer\"" ws ":" ws str ws "}"` + "\n" +
		`str ::= "\"" ( [^"\\] | "\\" ["\\/bfnrt] )* "\""` + "\n" + `ws ::= [ \t\n]*`
	variants := []struct {
		name   string
		cases  []probeCase
		format bool // response_format json_schema, strict, as ai sends it
		extra  func(probeCase) option.RequestOption
	}{
		{"no schema", []probeCase{required, enum}, false, nil},
		{"json_schema (Probe A)", []probeCase{required, enum}, true, nil},
		{"+ top-level json_schema", []probeCase{required, enum}, true, func(c probeCase) option.RequestOption { return option.WithJSONSet("json_schema", c.schema) }},
		{"+ top-level grammar", []probeCase{required}, true, func(probeCase) option.RequestOption { return option.WithJSONSet("grammar", grammar) }},
		{"json_object + schema", []probeCase{required, enum}, false, func(c probeCase) option.RequestOption {
			return option.WithJSONSet("response_format", map[string]any{"type": "json_object", "schema": c.schema})
		}},
	}
	client := openai.NewClient(option.WithBaseURL(cfg.ChatURL), option.WithAPIKey(cfg.ChatKey), option.WithMaxRetries(0))
	for _, v := range variants {
		for _, c := range v.cases {
			for rep := 1; rep <= nativeReps; rep++ {
				params := openai.ChatCompletionNewParams{Model: shared.ChatModel(cfg.ChatModel), MaxTokens: openai.Int(1024),
					Messages: []openai.ChatCompletionMessageParamUnion{openai.SystemMessage("You answer in JSON only."), openai.UserMessage(c.ask)}}
				params.StreamOptions.IncludeUsage = openai.Bool(true)
				if cfg.Temperature != nil {
					params.Temperature = openai.Float(*cfg.Temperature)
				}
				if cfg.ReasoningEffort != "" {
					params.ReasoningEffort = shared.ReasoningEffort(cfg.ReasoningEffort)
				}
				if v.format {
					params.ResponseFormat.OfJSONSchema = &shared.ResponseFormatJSONSchemaParam{JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
						Name: "probe", Schema: c.schema, Strict: openai.Bool(true)}}
				}
				var opts []option.RequestOption
				if v.extra != nil {
					opts = append(opts, v.extra(c))
				}
				native(t, &client, params, opts, v.name, c, rep)
			}
		}
	}
}

// nativeReps is how often each variant asks each case.
const nativeReps = 8

// native streams one request and logs one line.
func native(t *testing.T, client *openai.Client, params openai.ChatCompletionNewParams, opts []option.RequestOption, variant string, c probeCase, rep int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start, first := time.Now(), time.Duration(0)
	stream := client.Chat.Completions.NewStreaming(ctx, params, opts...)
	defer stream.Close()
	var acc openai.ChatCompletionAccumulator
	var text strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" && first == 0 {
				first = time.Since(start)
			}
			text.WriteString(ch.Delta.Content)
		}
	}
	if err := stream.Err(); err != nil {
		t.Logf("%-24s %-18s #%d error: %v", variant, c.name, rep, err)
		return
	}
	reply := text.String()
	broken := "not a JSON object"
	var doc map[string]any
	if json.Unmarshal([]byte(reply), &doc) == nil {
		broken = c.broken(doc)
	}
	format, err := ai.Dynamic[map[string]any]("probe", c.schema)
	if err != nil {
		t.Fatal(err)
	}
	_, derr := ai.Decode(format, reply)
	t.Logf("%-24s %-18s #%d obeyed=%-5v flagged=%-5v first %5d ms in %5d tokens reply=%.120q",
		variant, c.name, rep, broken == "", derr != nil, first.Milliseconds(), acc.Usage.PromptTokens, reply)
	if (derr != nil) != (broken != "") {
		t.Errorf("%s %s #%d: Decode flagged=%v but the separate check says %q", variant, c.name, rep, derr != nil, broken)
	}
}

// keyRe matches what may be a key in the server's own words.
var keyRe = regexp.MustCompile(`sk-[A-Za-z0-9_\-:]+`)

// props logs what the server's /props reports, any key masked.
func props(t *testing.T, url, key string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("GET /props: %v", err)
		return
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	t.Logf("GET /props: %s\n%s", res.Status, keyRe.ReplaceAllString(string(body), "sk-…"))
}
