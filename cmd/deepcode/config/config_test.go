package config

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// clean runs the test in an empty folder, with no DEEPCODE_* setting from the
// caller's environment (an empty variable counts as unset).
func clean(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DEEPCODE_CONFIG", "")
	for k := range defaults {
		t.Setenv("DEEPCODE_"+strings.ToUpper(k), "")
	}
}

func write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDefaults: no file and no variables give the defaults; embeddings use
// the chat endpoint and key.
func TestDefaults(t *testing.T) {
	clean(t)
	t.Setenv("DEEPCODE_CHAT_API_KEY", "k")
	cfg, addr, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if addr != "127.0.0.1:8080" || cfg.DataDir != "data" || cfg.ChatURL != "https://api.openai.com/v1" ||
		cfg.EmbedURL != cfg.ChatURL || cfg.EmbedKey != "k" || cfg.ChatModel != "" || cfg.ReasoningEffort != "" ||
		cfg.Workers != 2 || cfg.LLMTimeout != 5*time.Minute || cfg.JobTimeout != 2*time.Hour || cfg.SyncEvery != 0 ||
		cfg.CacheMB != 64 || cfg.MaxRepoMB != 1024 || cfg.MaxFiles != 20000 || cfg.MaxFileKB != 512 || cfg.MaxIndexMB != 200 ||
		cfg.WikiLanguage != "Traditional Chinese (Taiwan)" || cfg.SessionTTL != 720*time.Hour || cfg.GitProtocols != "http:https" || len(cfg.TrustedProxies) != 0 ||
		cfg.MaxOutputTokens != 0 || cfg.LLMRPM != 0 || cfg.WikiParallel != 1 || cfg.WikiMaxPages != 64 || cfg.Temperature != nil ||
		cfg.ChatAPI != "completions" {
		t.Errorf("defaults: addr %q, %+v", addr, cfg)
	}
}

// TestFileThenEnv: the file overrides the defaults, a variable overrides the
// file, and an empty variable, or an empty value in the file, counts as unset.
func TestFileThenEnv(t *testing.T) {
	clean(t)
	write(t, File, `
addr: ""
cache_mb: ""
chat_api_key: file-key
chat_model: file-model
chat_api: responses
embed_base_url: http://embed:9000/v1
reasoning_effort: none
temperature: 0.2
workers: 4
llm_timeout: 90s
sync_every: 0
trusted_proxies: [127.0.0.1, "10.0.0.0/8"]
`)
	t.Setenv("DEEPCODE_CHAT_MODEL", "")
	cfg, addr, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if addr != "127.0.0.1:8080" || cfg.CacheMB != 64 || cfg.Workers != 4 || cfg.EmbedKey != "file-key" {
		t.Errorf("empty values in the file: addr %q, %+v", addr, cfg)
	}
	t.Setenv("DEEPCODE_WORKERS", "8")
	cfg, _, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChatModel != "file-model" || cfg.ChatAPI != "responses" || cfg.EmbedURL != "http://embed:9000/v1" || cfg.ReasoningEffort != "none" ||
		cfg.Workers != 8 || cfg.LLMTimeout != 90*time.Second || cfg.SyncEvery != 0 || cfg.Temperature == nil || *cfg.Temperature != 0.2 ||
		!slices.Equal(cfg.TrustedProxies, []string{"127.0.0.1", "10.0.0.0/8"}) {
		t.Errorf("file then env: %+v", cfg)
	}

	// DEEPCODE_CONFIG names another file, in place of deepcode.yaml; a
	// variable's list splits on commas and spaces.
	write(t, "other.yml", "chat_model: other\n")
	t.Setenv("DEEPCODE_CONFIG", "other.yml")
	t.Setenv("DEEPCODE_TRUSTED_PROXIES", "127.0.0.1, ::1 10.0.0.0/8")
	cfg, _, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChatModel != "other" || cfg.Workers != 8 || cfg.LLMTimeout != 5*time.Minute ||
		!slices.Equal(cfg.TrustedProxies, []string{"127.0.0.1", "::1", "10.0.0.0/8"}) {
		t.Errorf("DEEPCODE_CONFIG: %+v", cfg)
	}
}

// TestBadValues: a value that is set but unusable stops the start, naming
// where it was set; every one of them is reported, and no secret is shown.
func TestBadValues(t *testing.T) {
	for _, c := range []struct {
		name, file string
		env        map[string]string
		want       []string
		hide       string // must not be in the error
	}{
		{name: "zero workers", env: map[string]string{"DEEPCODE_WORKERS": "0"}, want: []string{`DEEPCODE_WORKERS="0": want a whole number from 1 to 1073741824`}},
		{name: "negative tokens", env: map[string]string{"DEEPCODE_MAX_OUTPUT_TOKENS": "-1"}, want: []string{`DEEPCODE_MAX_OUTPUT_TOKENS="-1": want a whole number from 0 to`}},
		{name: "too big", file: "max_repo_mb: 9000000000000\n", want: []string{`max_repo_mb in deepcode.yaml="9000000000000": want a whole number from 1 to`}},
		{name: "not a number", file: "cache_mb: lots\n", want: []string{`cache_mb in deepcode.yaml="lots"`}},
		{name: "duration without unit", file: "llm_timeout: 300\n", want: []string{`llm_timeout in deepcode.yaml="300": want a duration`}},
		{name: "zero timeout", env: map[string]string{"DEEPCODE_JOB_TIMEOUT": "0"}, want: []string{`DEEPCODE_JOB_TIMEOUT="0"`}},
		{name: "sync too often", env: map[string]string{"DEEPCODE_SYNC_EVERY": "30s"}, want: []string{`DEEPCODE_SYNC_EVERY="30s": want 0 (off) or 1m or more`}},
		{name: "effort", env: map[string]string{"DEEPCODE_REASONING_EFFORT": "max"}, want: []string{`DEEPCODE_REASONING_EFFORT="max": want one of none, minimal, low`}},
		{name: "api", env: map[string]string{"DEEPCODE_CHAT_API": "chat"}, want: []string{`DEEPCODE_CHAT_API="chat": want one of completions, responses`}},
		{name: "temperature", env: map[string]string{"DEEPCODE_TEMPERATURE": "3"}, want: []string{`DEEPCODE_TEMPERATURE="3": want a number from 0 to 2`}},
		{name: "temperature text", file: "temperature: warm\n", want: []string{`temperature in deepcode.yaml="warm": want a number from 0 to 2`}},
		{name: "temperature NaN", env: map[string]string{"DEEPCODE_TEMPERATURE": "NaN"}, want: []string{`DEEPCODE_TEMPERATURE="NaN": want a number from 0 to 2`}},
		{name: "url without scheme", env: map[string]string{"DEEPCODE_CHAT_BASE_URL": "localhost:8080/v1"}, want: []string{`DEEPCODE_CHAT_BASE_URL="(not shown, as it may hold a key)": want an http or https URL`}},
		{name: "embed url", file: "embed_base_url: ftp://host/v1\n", want: []string{`embed_base_url in deepcode.yaml="ftp://host": want an http or https URL`}},
		{name: "list for a value", file: "chat_model: [a, b]\n", want: []string{`chat_model in deepcode.yaml="[a b]": want text`}},
		{name: "list for a number", file: "workers: [1, 2]\n", want: []string{`workers in deepcode.yaml="[1 2]": want a single value`}},
		{name: "unquoted number for text", file: "admin_password: 12345678\nsecret_key: 0123\n",
			want: []string{"admin_password in deepcode.yaml: want text, in quotes", "secret_key in deepcode.yaml: want text"}, hide: "12345678"},
		{name: "number for a url", file: "embed_base_url: [your-key]\n", want: []string{`embed_base_url in deepcode.yaml="(not shown`}, hide: "your-key"},
		{name: "yaml quotes a value", file: "your-key-on-its-own\n", want: []string{"config file deepcode.yaml:"}, hide: "your-ke"},
		{name: "yaml quotes an alias", file: "admin_password: *your-key\n", want: []string{"config file deepcode.yaml:"}, hide: "your-key"},
		{name: "yaml quotes a key", file: "your-key: 1\nyour-key: 2\n", want: []string{"config file deepcode.yaml:"}, hide: "your-key"},
		{name: "short sessions", env: map[string]string{"DEEPCODE_SESSION_TTL": "30s"}, want: []string{`DEEPCODE_SESSION_TTL="30s": want 1m or more`}},
		{name: "secret list", file: "chat_api_key: [your-key, other-key]\n", want: []string{"chat_api_key in deepcode.yaml: want text"}, hide: "your-key"},
		{name: "embed secret", file: "embed_api_key: [your-key]\n", want: []string{"embed_api_key in deepcode.yaml: want text"}, hide: "your-key"},
		{name: "bad addr", file: "addr: 8080\n", want: []string{`addr in deepcode.yaml="8080": want text`}},
		{name: "url credentials", file: "chat_base_url: ftp://user:your-key@host/v1\n", want: []string{`chat_base_url in deepcode.yaml="ftp://host"`}, hide: "your-key"},
		{name: "url key in query", file: "chat_base_url: ftp://host/v1?api-key=your-key\n", want: []string{`chat_base_url in deepcode.yaml="ftp://host"`}, hide: "your-key"},
		{name: "url without host", file: "chat_base_url: http://:80/v1\n", want: []string{`chat_base_url in deepcode.yaml="http://:80": want an http or https URL`}},
		{name: "nested proxies", file: "trusted_proxies: [[1, 2]]\n", want: []string{`trusted_proxies in deepcode.yaml="[[1 2]]": want addresses or CIDRs`}},
		{name: "url that is a key", env: map[string]string{"DEEPCODE_CHAT_BASE_URL": "your-key@host/v1"}, want: []string{`DEEPCODE_CHAT_BASE_URL="(not shown`}, hide: "your-key"},
		{name: "unparsable url credentials", env: map[string]string{"DEEPCODE_EMBED_BASE_URL": "http://user:your-key@host:port/v1"},
			want: []string{`DEEPCODE_EMBED_BASE_URL="(not shown`}, hide: "your-key"},
		{name: "unknown key", file: "chat_modle: x\nchat:\n  model: y\n", want: []string{"unknown setting chat.model, chat_modle"}},
		{name: "not yaml", file: "chat_model: [\n", want: []string{"config file deepcode.yaml:"}},
		{name: "named file missing", env: map[string]string{"DEEPCODE_CONFIG": "missing.yaml"}, want: []string{"config file missing.yaml:"}},
		{name: "several", file: "workers: 0\n", env: map[string]string{"DEEPCODE_LLM_RPM": "x"},
			want: []string{`DEEPCODE_LLM_RPM="x"`, `workers in deepcode.yaml="0"`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			clean(t)
			if c.file != "" {
				write(t, File, c.file)
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, _, err := Load()
			for _, w := range c.want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Errorf("got %v, want it to hold %q", err, w)
				}
			}
			if c.hide != "" && err != nil && strings.Contains(err.Error(), c.hide) {
				t.Errorf("%v shows %q", err, c.hide)
			}
		})
	}
}

// TestDataDir: backup and passwd read the data directory past bad values and
// unknown keys elsewhere, but not past a bad one of its own or a broken file.
func TestDataDir(t *testing.T) {
	clean(t)
	write(t, File, "data: /srv/deepcode\nworkers: 0\nchat_modle: x\n")
	t.Setenv("DEEPCODE_LLM_TIMEOUT", "300")
	if dir, err := DataDir(); dir != "/srv/deepcode" || err != nil {
		t.Errorf("DataDir() = %q, %v", dir, err)
	}
	t.Setenv("DEEPCODE_DATA", "/env")
	if dir, err := DataDir(); dir != "/env" || err != nil {
		t.Errorf("DataDir() with DEEPCODE_DATA = %q, %v", dir, err)
	}
	t.Setenv("DEEPCODE_DATA", "")
	for _, body := range []string{"data: [a, b]\n", "data: [\n"} {
		write(t, File, body)
		if _, err := DataDir(); err == nil {
			t.Errorf("DataDir() with %q: no error", body)
		}
	}
}
