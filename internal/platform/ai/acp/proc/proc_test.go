package proc

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMain runs this test binary as the processes the tests start: procparent
// starts procbeat and reads its standard input, ending at its end or never;
// procbeat writes a byte to a file every 20 ms till it is ended; procsay
// writes its argument and ends.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 {
		switch os.Args[1] {
		case "procsay":
			os.Stdout.WriteString(os.Args[2])
			os.Exit(0)
		case "procparent":
			child := exec.Command(os.Args[0], "procbeat", os.Args[2])
			if err := child.Start(); err != nil {
				os.Exit(2)
			}
			for s := bufio.NewScanner(os.Stdin); s.Scan(); {
			}
			if os.Args[3] == "ends" {
				os.Exit(0)
			}
			time.Sleep(time.Hour) // not select {}: the runtime ends a process whose goroutines all block
		case "procbeat":
			for {
				f, err := os.OpenFile(os.Args[2], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
				if err == nil {
					f.WriteString(".")
					f.Close()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
	os.Exit(m.Run())
}

// TestSplit: words split at spaces, a quoted part whole, a backslash kept.
func TestSplit(t *testing.T) {
	for line, want := range map[string][]string{
		"npx -y @agentclientprotocol/claude-agent-acp":    {"npx", "-y", "@agentclientprotocol/claude-agent-acp"},
		`"C:\Program Files\nodejs\npx.cmd"  -y  pkg `:     {`C:\Program Files\nodejs\npx.cmd`, "-y", "pkg"},
		`agent --name="two words" ""`:                     {"agent", "--name=two words", ""},
		"   ":                                             nil,
		"opencode\tacp":                                   {"opencode", "acp"},
		`C:\tools\gemini.exe --acp`:                       {`C:\tools\gemini.exe`, "--acp"},
		`copilot --acp --stdio "--model=gpt 6" tail"ing"`: {"copilot", "--acp", "--stdio", "--model=gpt 6", "tailing"},
	} {
		if got, err := Split(line); err != nil || !slices.Equal(got, want) {
			t.Errorf("Split(%q) = %q, %v; want %q", line, got, err, want)
		}
	}
	if _, err := Split(`npx "unclosed`); !errors.Is(err, ErrLine) {
		t.Errorf("an unclosed quote: %v", err)
	}
	if _, err := Start("  ", nil, nil); !errors.Is(err, ErrLine) {
		t.Errorf("no command: %v", err)
	}
}

// TestName: an agent's name is the package a runner runs or the program's,
// never a flag's value, which may hold a key.
func TestName(t *testing.T) {
	for line, want := range map[string]string{
		"npx -y @agentclientprotocol/claude-agent-acp@0.107.3": "claude-agent-acp", "gemini --acp": "gemini",
		`"C:\Program Files\nodejs\npx.cmd" -y @agentclientprotocol/codex-acp`: "codex-acp", "pnpm dlx opencode-ai acp": "opencode-ai",
		`C:\bin\opencode.exe acp`: "opencode", "": "acp", `C:\bin\ --acp`: "acp", "bunx --token=k3y codex-acp": "codex-acp",
		"npx -y --registry https://user:k3y@registry.example/npm/ @agentclientprotocol/claude-agent-acp": "claude-agent-acp",

		"npx --token k3y @agentclientprotocol/codex-acp@2.6.0": "codex-acp", // a scoped package after a flag's value
		"npx --registry user:k3y@host codex-acp":               "codex-acp", // a value with a colon
		"npx codex-acp @notes":                                 "codex-acp", // an argument, not a scoped package
		"npx":                                                  "npx",
		"npx --token k3y codex-acp":                            "codex-acp", // a flag's value given apart from it
		"npx --no-such-flag codex-acp":                         "npx",       // as a value it might be: not taken
		"npx -y --yes codex-acp@2.6.0":                         "codex-acp", // switches take no value
		"uvx --quiet agent-tool==1.2":                          "agent-tool",
		"npx @notes codex-acp":                                 "codex-acp",
		"npx --no-install -- codex-acp":                        "codex-acp",
	} {
		if got := Name(line); got != want {
			t.Errorf("%q: %s, want %s", line, got, want)
		}
	}
}

// TestEnv: an agent gets the OS's, npm's and the proxy's variables and what is
// added, never deepcode's keys nor a host agent's.
func TestEnv(t *testing.T) {
	for k, v := range map[string]string{"DS_APIKEY": "ds", "DEEPCODE_CHAT_API_KEY": "dc", "OPENAI_API_KEY": "oa", "ANTHROPIC_API_KEY": "an",
		"CLAUDECODE": "1", "NPM_CONFIG_REGISTRY": "https://registry.example", "HTTPS_PROXY": "http://proxy:1"} {
		t.Setenv(k, v)
	}
	env := Env("CODEX_HOME=/h")
	has := func(name string) bool {
		return slices.ContainsFunc(env, func(kv string) bool { return strings.EqualFold(strings.SplitN(kv, "=", 2)[0], name) })
	}
	for _, name := range []string{"PATH", "NPM_CONFIG_REGISTRY", "HTTPS_PROXY", "CODEX_HOME"} {
		if !has(name) {
			t.Errorf("%s not passed: %q", name, env)
		}
	}
	for _, name := range []string{"DS_APIKEY", "DEEPCODE_CHAT_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CLAUDECODE"} {
		if has(name) {
			t.Errorf("%s passed", name)
		}
	}
	if env[len(env)-1] != "CODEX_HOME=/h" {
		t.Errorf("the added variable is not last, where it wins: %q", env)
	}
}

// TestLastWords: what a process wrote before it ended is read after it ended.
func TestLastWords(t *testing.T) {
	p, err := Start(`"`+os.Args[0]+`" procsay bye`, Env(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(time.Second)
	<-p.Done()
	if b, err := io.ReadAll(p.Stdout); err != nil || string(b) != "bye" {
		t.Errorf("after the process ended: %q, %v", b, err)
	}
}

// TestStopEndsTree: Stop ends the process and the child it started, whether it
// ends on its own once its input closes (as Codex's adapter does, leaving git
// behind) or not at all.
func TestStopEndsTree(t *testing.T) {
	for _, ends := range []string{"ends", "stays"} {
		t.Run(ends, func(t *testing.T) {
			beat := filepath.Join(t.TempDir(), "beat")
			p, err := Start(`"`+os.Args[0]+`" procparent "`+beat+`" `+ends, Env(), nil)
			if err != nil {
				t.Fatal(err)
			}
			size := func() int64 {
				fi, err := os.Stat(beat)
				if err != nil {
					return 0
				}
				return fi.Size()
			}
			for deadline := time.Now().Add(20 * time.Second); size() < 2; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the child never ran")
				}
			}
			start := time.Now()
			p.Stop(300 * time.Millisecond)
			if took := time.Since(start); ends == "stays" && took < 300*time.Millisecond || took > 10*time.Second {
				t.Errorf("Stop took %v", took)
			}
			select {
			case <-p.Done():
			default:
				t.Error("Done is open after Stop")
			}
			time.Sleep(200 * time.Millisecond)
			before := size()
			time.Sleep(300 * time.Millisecond)
			if after := size(); after != before {
				t.Errorf("the child still runs: %d bytes, then %d", before, after)
			}
		})
	}
}
