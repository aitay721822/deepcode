package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"deepcode/internal/app"
)

// loadConfig reads the DEEPCODE_* environment variables; README.md lists them.
// A number or duration that is set but cannot be used stops the start, naming
// the variable: a mistyped limit must not quietly become the default.
func loadConfig() (app.Config, error) {
	var bad []error
	chatURL := env("DEEPCODE_CHAT_BASE_URL", "https://api.openai.com/v1")
	chatKey := os.Getenv("DEEPCODE_CHAT_API_KEY")
	cfg := app.Config{
		DataDir: env("DEEPCODE_DATA", "data"),

		ChatURL: chatURL, ChatKey: chatKey, ChatModel: os.Getenv("DEEPCODE_CHAT_MODEL"),
		EmbedURL:        env("DEEPCODE_EMBED_BASE_URL", chatURL),
		EmbedKey:        env("DEEPCODE_EMBED_API_KEY", chatKey),
		EmbedModel:      os.Getenv("DEEPCODE_EMBED_MODEL"),
		ReasoningEffort: os.Getenv("DEEPCODE_REASONING_EFFORT"),
		MaxOutputTokens: envInt(&bad, "DEEPCODE_MAX_OUTPUT_TOKENS", 0),
		LLMTimeout:      envDuration(&bad, "DEEPCODE_LLM_TIMEOUT", 5*time.Minute),
		LLMRPM:          envInt(&bad, "DEEPCODE_LLM_RPM", 0),

		Workers:    envInt(&bad, "DEEPCODE_WORKERS", 2),
		JobTimeout: envDuration(&bad, "DEEPCODE_JOB_TIMEOUT", 2*time.Hour),
		CacheMB:    envInt(&bad, "DEEPCODE_CACHE_MB", 64),

		MaxRepoMB:  envInt(&bad, "DEEPCODE_MAX_REPO_MB", 1024),
		MaxFiles:   envInt(&bad, "DEEPCODE_MAX_FILES", 20000),
		MaxFileKB:  envInt(&bad, "DEEPCODE_MAX_FILE_KB", 512),
		MaxIndexMB: envInt(&bad, "DEEPCODE_MAX_INDEX_MB", 200),

		WikiLanguage: env("DEEPCODE_WIKI_LANGUAGE", "English"),
		GitProtocols: "http:https",

		AdminPassword: os.Getenv("DEEPCODE_ADMIN_PASSWORD"),
		SecretKey:     os.Getenv("DEEPCODE_SECRET_KEY"),
		SessionTTL:    envDuration(&bad, "DEEPCODE_SESSION_TTL", 30*24*time.Hour),

		TrustedProxies: strings.FieldsFunc(os.Getenv("DEEPCODE_TRUSTED_PROXIES"), func(r rune) bool { return r == ',' || unicode.IsSpace(r) }),
	}
	return cfg, errors.Join(bad...)
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// envInt reads a whole number above 0, or 0 where 0 is the default (no
// limit): 0 workers or a 0 file limit is never meant.
func envInt(bad *[]error, name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || (n == 0 && def != 0) {
		*bad = append(*bad, fmt.Errorf("%s=%q: want a whole number above 0", name, v))
	}
	return n
}

func envDuration(bad *[]error, name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		*bad = append(*bad, fmt.Errorf("%s=%q: want a duration above 0, such as 90m or 2h", name, v))
	}
	return d
}
