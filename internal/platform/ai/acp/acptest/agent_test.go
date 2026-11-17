// Black-box tests of package acp: an acp.Agent as its callers see it, on
// this test binary run as the fake agent.
package acptest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/acp"
	"deepcode/internal/platform/ai/acp/acptest"
	"deepcode/internal/platform/ai/usage"
)

// TestMain runs this test binary as the fake agent when asked to.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "acpfake" {
		acptest.Main(os.Args[2:])
	}
	os.Exit(m.Run())
}

// fake is an acp.Agent on this test binary run as the fake agent with flags,
// set as set says, closed when the test ends.
func fake(t *testing.T, flags string, set ...func(*acp.Agent)) *acp.Agent {
	a := &acp.Agent{Command: `"` + os.Args[0] + `" acpfake ` + flags, Dir: t.TempDir(), Timeout: time.Minute, Count: acptest.Quarter, Meter: &acptest.Metered{}}
	for _, f := range set {
		f(a)
	}
	t.Cleanup(a.Close)
	return a
}

// TestSessions: each request is a session in its repository's folder, its
// instructions where Claude's, Codex's or any other adapter takes them, the
// conversation as a transcript, the agent's environment without deepcode's keys,
// the user's settings and connectors off unless asked for, and Codex in its
// read-only mode.
func TestSessions(t *testing.T) {
	t.Setenv("DS_APIKEY", "secret")
	t.Setenv("DEEPCODE_CHAT_API_KEY", "secret")
	p := ai.Prompt{Static: "Be brief.", Previous: []ai.Message{{Role: "user", Content: "q1"}, {Role: "assistant", Content: "a1"}}, Dynamic: "q2"}
	claude := fake(t, "-name claude-agent-acp", func(a *acp.Agent) { a.CodexHome = "/codex" })
	got := acptest.Echo(t, usage.WithRepository(context.Background(), 7), claude, p)
	if acptest.At(got, "static") != "Be brief." || filepath.Base(acptest.At(got, "cwd").(string)) != "7" || acptest.At(got, "air") != true ||
		!strings.HasPrefix(acptest.At(got, "prompt").(string), "<conversation>\n<user>\nq1\n</user>\n<assistant>\na1\n</assistant>\n</conversation>\n\nq2") {
		t.Fatalf("claude: %v", got)
	}
	elsewhere := acptest.Echo(t, usage.WithRepository(context.Background(), 8), claude, ai.Prompt{Dynamic: "q3"}) // another project's: a new session, in its folder
	if filepath.Base(acptest.At(elsewhere, "cwd").(string)) != "8" || acptest.At(elsewhere, "opened") != acptest.At(got, "opened").(float64)+1 ||
		acptest.At(elsewhere, "open") != 1.0 {
		t.Errorf("another repository's request: %v", elsewhere)
	}
	options := acptest.At(got, "meta", "claudeCode", "options")
	if b, _ := json.Marshal(options); string(b) != `{"promptSuggestions":false,"settingSources":[],"settings":{"autoMemoryEnabled":false,"disableClaudeAiConnectors":true},"strictMcpConfig":true,"tools":[]}` {
		t.Errorf("claude's options: %s", b)
	}
	env := acptest.At(got, "env").(map[string]any)
	if env["DS_APIKEY"] != "" || env["DEEPCODE_CHAT_API_KEY"] != "" || env["PATH"] == "" || env["CODEX_HOME"] != "/codex" ||
		env["CODEX_CONFIG"] != `{"features":{"shell_tool":false,"skip_host_skill_discovery":true,"unified_exec":false},"include_apps_instructions":false,`+
			`"include_collaboration_mode_instructions":false,"include_environment_context":false,"include_permissions_instructions":false,"model_verbosity":"medium","skills":{"include_instructions":false}}` {
		t.Errorf("the agent's environment: %v", env)
	}
	if got := acptest.Echo(t, context.Background(), claude, ai.Prompt{}); filepath.Base(acptest.At(got, "cwd").(string)) != "_" || !filepath.IsAbs(acptest.At(got, "cwd").(string)) ||
		acptest.At(got, "static") != "Answer the request." {
		t.Errorf("a request of no repository and no instructions: %v", got)
	}

	mine := fake(t, "-name claude-agent-acp", func(a *acp.Agent) { a.UserConfig, a.Connectors = true, true })
	got = acptest.Echo(t, context.Background(), mine, p)
	if b, _ := json.Marshal(acptest.At(got, "meta", "claudeCode", "options")); string(b) != `{"promptSuggestions":false,"settingSources":["user","project","local"],"settings":{"autoMemoryEnabled":true,"disableClaudeAiConnectors":false},"strictMcpConfig":false,"tools":[]}` ||
		acptest.At(got, "env", "CODEX_CONFIG") != `{"features":{"shell_tool":false,"unified_exec":false},"include_collaboration_mode_instructions":false,`+
			`"include_environment_context":false,"include_permissions_instructions":false,"model_verbosity":"medium"}` || acptest.At(got, "env", "CODEX_HOME") != "" {
		t.Errorf("the user's settings and connectors asked for: %s, %v", b, acptest.At(got, "env"))
	}

	codex := fake(t, "-name codex-acp -modes agent,read-only")
	got = acptest.Echo(t, context.Background(), codex, p)
	if acptest.At(got, "static") != "Be brief." || acptest.At(got, "meta", "jetbrains", "air", "customInstructions") != "Be brief." || acptest.At(got, "config", "mode") != "read-only" {
		t.Errorf("codex: %v", got)
	}

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	other := fake(t, "-name gemini-cli", func(a *acp.Agent) { a.CodexHome, a.Connectors = "/codex", true })
	got = acptest.Echo(t, context.Background(), other, p)
	if acptest.At(got, "meta") != nil || !strings.HasPrefix(acptest.At(got, "prompt").(string), "<instructions>\nBe brief.\n</instructions>\n\n<conversation>") {
		t.Errorf("another agent: %v", got)
	}
	ai.Generate(context.Background(), other, ai.Prompt{Dynamic: "fake:tool"}) // what it asks for, each logged cut to 80 characters
	if s := logged.String(); !strings.Contains(s, "acp_codex_home is not used") || !strings.Contains(s, "acp_claude_connectors is not used") ||
		!strings.Contains(s, "neither Claude nor Codex") || strings.Count(s, strings.Repeat("x", 75)) != 5 || strings.Contains(s, "Long") {
		t.Errorf("the settings another agent leaves unused, the tools it asked for: %q", s)
	}
}

// TestTurns: no more prompts run at a time than Sessions; a request waiting
// for its turn gives up with its context.
func TestTurns(t *testing.T) {
	a := fake(t, "", func(a *acp.Agent) { a.Sessions = 1 })
	if _, err := ai.Generate(context.Background(), a, ai.Prompt{}); err != nil { // started
		t.Fatal(err)
	}
	slow, stop := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { _, err := ai.Generate(slow, a, ai.Prompt{Dynamic: "fake:slow"}); done <- err }()
	acptest.Await(t, a.Dir, "a slow prompt")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := ai.Generate(ctx, a, ai.Prompt{}); !errors.Is(err, context.DeadlineExceeded) || a.Status().Trouble != "" { // deepcode's queue, not the agent
		t.Errorf("a second prompt with one turn: %v, %+v", err, a.Status())
	}
	stop()
	<-done
	if text, err := ai.Generate(context.Background(), a, ai.Prompt{}); err != nil || text != "OK" {
		t.Errorf("once the turn is back: %q, %v", text, err)
	}
}
