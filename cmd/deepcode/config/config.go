// Package config reads deepcode's settings through Viper. Each source
// overrides the one before: the defaults below, the optional config file, the
// DEEPCODE_* environment variables; the commands' flags override all three.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cast"
	"github.com/spf13/viper"

	"deepcode/internal/app"
)

// File is the config file read when DEEPCODE_CONFIG names none; it may be
// missing. The extension picks the format: .yaml, .yml, .json, .toml, ...
const File = "deepcode.yaml"

// defaults holds every setting, by its key in the config file; its
// environment variable is DEEPCODE_ and the key in capitals. README.md lists
// them. A key the file holds that is not here stops serve.
var defaults = map[string]any{
	"addr": "127.0.0.1:8080",
	"data": "data",

	"chat_base_url":     "https://api.openai.com/v1",
	"chat_api_key":      "",
	"chat_model":        "", // "" turns chat and the wiki off
	"chat_api":          "completions",
	"embed_base_url":    "",  // "" = chat_base_url
	"embed_api_key":     "",  // "" = chat_api_key
	"embed_model":       "",  // "" turns vector search off
	"reasoning_effort":  "",  // "" leaves it to the server
	"temperature":       nil, // unset leaves it to the server; a number, as 0.2, in the file
	"max_output_tokens": 0,   // 0 leaves it to the server
	"llm_timeout":       5 * time.Minute,
	"llm_rpm":           0, // 0 = unlimited

	"workers":     2,
	"job_timeout": 2 * time.Hour,
	"cache_mb":    64,
	"sync_every":  time.Duration(0), // 0 = never

	"max_repo_mb":  1024,
	"max_files":    20000,
	"max_file_kb":  512,
	"max_index_mb": 200,

	"wiki_language":   "Traditional Chinese (Taiwan)",
	"wiki_parallel":   1,  // pages planned or written at a time
	"wiki_max_pages":  64, // the most pages a wiki gets
	"admin_password":  "",
	"secret_key":      "",
	"session_ttl":     30 * 24 * time.Hour,
	"trusted_proxies": "", // comma or space separated, or a list in the file
}

// efforts are the reasoning_effort values OpenAI-compatible servers take.
var efforts = []string{"none", "minimal", "low", "medium", "high", "xhigh"}

// apis are the APIs a chat model can be asked by: Chat Completions and the
// Responses API (ai.Completions, ai.Responses).
var apis = []string{"completions", "responses"}

// secrets are never shown in an error, which ends up in the log.
var secrets = []string{"chat_api_key", "embed_api_key", "admin_password", "secret_key"}

// most bounds every whole number, so that MB << 20 cannot overflow.
const most = 1 << 30

// Load reads the settings; addr is the listen address, which only serve
// uses. Every value that is set but cannot be used is reported, naming where
// it was set: a mistyped limit must not quietly become the default.
func Load() (cfg app.Config, addr string, err error) {
	v, err := read()
	if err != nil {
		return app.Config{}, "", err
	}
	var unknown []string
	for _, k := range v.AllKeys() { // the defaults' keys and the file's
		if _, ok := defaults[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return app.Config{}, "", fmt.Errorf("config file %s: unknown setting %s (README.md lists them)", v.ConfigFileUsed(), strings.Join(unknown, ", "))
	}
	p := &parser{v: v}
	chatURL := p.url("chat_base_url")
	chatKey := p.str("chat_api_key")
	list, err := cast.ToStringSliceE(v.Get("trusted_proxies")) // a list in the file, else text
	if err != nil {
		p.fail("trusted_proxies", fmt.Sprint(v.Get("trusted_proxies")), "addresses or CIDRs, as a list or comma separated")
	}
	var proxies []string
	for _, s := range list {
		proxies = append(proxies, strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })...)
	}
	cfg = app.Config{
		DataDir: p.str("data"),

		ChatURL: chatURL, ChatKey: chatKey, ChatModel: p.str("chat_model"),
		ChatAPI:         p.oneOf("chat_api", apis),
		EmbedURL:        cmp.Or(p.url("embed_base_url"), chatURL),
		EmbedKey:        cmp.Or(p.str("embed_api_key"), chatKey),
		EmbedModel:      p.str("embed_model"),
		ReasoningEffort: p.oneOf("reasoning_effort", efforts),
		Temperature:     p.temperature("temperature"),
		MaxOutputTokens: p.whole("max_output_tokens"),
		LLMTimeout:      p.duration("llm_timeout"),
		LLMRPM:          p.whole("llm_rpm"),

		Workers:    p.whole("workers"),
		JobTimeout: p.duration("job_timeout"),
		CacheMB:    p.whole("cache_mb"),
		SyncEvery:  p.duration("sync_every"),

		MaxRepoMB:  p.whole("max_repo_mb"),
		MaxFiles:   p.whole("max_files"),
		MaxFileKB:  p.whole("max_file_kb"),
		MaxIndexMB: p.whole("max_index_mb"),

		WikiLanguage: p.str("wiki_language"),
		WikiParallel: p.whole("wiki_parallel"),
		WikiMaxPages: p.whole("wiki_max_pages"),
		GitProtocols: "http:https",

		AdminPassword: p.str("admin_password"),
		SecretKey:     p.str("secret_key"),
		SessionTTL:    p.duration("session_ttl"),

		TrustedProxies: proxies,
	}
	if cfg.SyncEvery > 0 && cfg.SyncEvery < time.Minute { // each round asks every repository's git server
		p.fail("sync_every", v.GetString("sync_every"), "0 (off) or 1m or more")
	}
	if cfg.SessionTTL > 0 && cfg.SessionTTL < time.Minute { // sessions expire in whole seconds
		p.fail("session_ttl", v.GetString("session_ttl"), "1m or more")
	}
	addr = p.str("addr") // before p.bad is read: Go leaves that order open in one return
	return cfg, addr, errors.Join(p.bad...)
}

// DataDir reads only the data directory, for backup and passwd without
// --data: a bad value in another setting must not stand in the way of a backup
// or a password reset. A config file that cannot be read still stops them.
func DataDir() (string, error) {
	v, err := read()
	if err != nil {
		return "", err
	}
	p := &parser{v: v}
	dir := p.str("data") // before p.bad is read, as in Load
	return dir, errors.Join(p.bad...)
}

// read sets Viper up and reads the config file: the one DEEPCODE_CONFIG
// names, which must exist, else deepcode.yaml when there is one. An empty
// environment variable counts as unset, as it always has.
func read() (*viper.Viper, error) {
	v := viper.New()
	for k, d := range defaults {
		v.SetDefault(k, d)
	}
	v.SetEnvPrefix("DEEPCODE")
	v.AutomaticEnv()
	named := os.Getenv("DEEPCODE_CONFIG")
	path := cmp.Or(named, File)
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		if named == "" && errors.Is(err, fs.ErrNotExist) {
			return v, nil
		}
		// A parser quotes what it cannot place (a value, an alias), which may
		// be a key: keep the line number, drop anything quoted.
		return nil, fmt.Errorf("config file %s: %s", path, quoted.ReplaceAllString(err.Error(), "…"))
	}
	return v, nil
}

var quoted = regexp.MustCompile("`[^`]*`|'[^']*'|\"[^\"]*\"")

// parser turns the raw values into typed ones, collecting what cannot be used.
type parser struct {
	v   *viper.Viper
	bad []error
}

// fail records that key's value cannot be used, naming where it was set: the
// environment variable, else the config file (a default is always usable).
// A secret's value is left out.
func (p *parser) fail(key, val, want string) {
	where := key + " in " + p.v.ConfigFileUsed()
	if env := "DEEPCODE_" + strings.ToUpper(key); os.Getenv(env) != "" {
		where = env
	}
	if !slices.Contains(secrets, key) {
		where += fmt.Sprintf("=%q", val)
	}
	p.bad = append(p.bad, fmt.Errorf("%s: want %s", where, want))
}

// get reads key as text; ok is false, and the failure recorded, for a list
// or a table in the file, or for a text setting the file gives as a number or
// true/false: YAML reads an unquoted 0123 as 83, so it must be quoted. An
// empty value in the file counts as unset, as in the environment.
func (p *parser) get(key string) (s string, ok bool) {
	raw := p.v.Get(key)
	if raw == "" {
		raw = defaults[key]
	}
	shown := fmt.Sprint(raw)
	if strings.HasSuffix(key, "_url") {
		shown = notShown
	}
	if _, text := defaults[key].(string); text {
		if s, ok := raw.(string); ok {
			return s, true
		}
		p.fail(key, shown, "text, in quotes if it looks like a number or true/false")
		return "", false
	}
	s, err := cast.ToStringE(raw)
	if err != nil {
		p.fail(key, shown, "a single value")
		return "", false
	}
	return s, true
}

// notShown stands for a URL that may hold a key.
const notShown = "(not shown, as it may hold a key)"

func (p *parser) str(key string) string {
	s, _ := p.get(key)
	return s
}

// whole reads a whole number above 0, or 0 where 0 is the default (no
// limit): 0 workers or a 0 file limit is never meant.
func (p *parser) whole(key string) int {
	s, ok := p.get(key)
	n, err := strconv.Atoi(s)
	least := 1
	if defaults[key] == 0 {
		least = 0
	}
	if ok && (err != nil || n < least || n > most) {
		p.fail(key, s, fmt.Sprintf("a whole number from %d to %d", least, most))
	}
	return n
}

// duration reads a duration above 0 with its unit, or 0 where 0 is the
// default (off): a bare number in the file is not taken as nanoseconds.
func (p *parser) duration(key string) time.Duration {
	s, ok := p.get(key)
	d, err := time.ParseDuration(s)
	if ok && (err != nil || d < 0 || d == 0 && defaults[key] != time.Duration(0)) {
		p.fail(key, s, "a duration above 0 with its unit, such as 90m or 2h")
	}
	return d
}

// url reads an http or https endpoint; "" stays "" where it is the default.
func (p *parser) url(key string) string {
	s, ok := p.get(key)
	if s == "" && defaults[key] == "" {
		return ""
	}
	u, err := url.Parse(s)
	if ok && (err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "") {
		shown := notShown
		if err == nil && u.Scheme != "" && u.Host != "" {
			shown = u.Scheme + "://" + u.Host // no user, path or query, any of which may hold a key
		}
		p.fail(key, shown, "an http or https URL, such as https://api.openai.com/v1")
	}
	return s
}

// temperature reads a sampling temperature from 0 to 2, or nil for "".
func (p *parser) temperature(key string) *float64 {
	s, ok := p.get(key)
	if s == "" {
		return nil
	}
	t, err := strconv.ParseFloat(s, 64)
	if ok && (err != nil || !(t >= 0 && t <= 2)) { // NaN too
		p.fail(key, s, "a number from 0 to 2, such as 0.2")
		return nil
	}
	return &t
}

// oneOf reads one of the allowed values, or "".
func (p *parser) oneOf(key string, allowed []string) string {
	s, _ := p.get(key)
	if s != "" && !slices.Contains(allowed, s) {
		p.fail(key, s, "one of "+strings.Join(allowed, ", "))
	}
	return s
}
