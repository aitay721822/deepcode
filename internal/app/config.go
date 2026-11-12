package app

import (
	"time"

	"deepcode/internal/platform/ai"
)

// Config is everything the application needs; cmd/deepcode/config fills it
// from the config file and DEEPCODE_* variables, and flags override that.
type Config struct {
	DataDir string // holds deepcode.db, secret.key and repos/ (the git fetch cache)

	ChatURL, ChatKey, ChatModel    string   // ChatModel "" turns chat and the wiki off
	ChatAPI                        string   // ai.Completions or ai.Responses: how the chat model is asked
	ChatSchema                     string   // ai.SchemaServer or ai.SchemaPrompt: where a request for JSON gives its schema
	EmbedURL, EmbedKey, EmbedModel string   // EmbedModel "" turns vector search off
	ReasoningEffort                string   // sent as reasoning_effort when set
	Temperature                    *float64 // sent as temperature when set
	MaxOutputTokens                int      // 0 leaves it to the server
	ChatContext                    int      // the chat model's context in tokens; 0: unknown (ai.OpenAI.Window)
	CompactRatio                   float64  // the share of it a request fills before it is made smaller
	ChatTokenizer                  string   // the chat model's tokenizer.json; "" counts its tokens by o200k_base (harness.LoadTokens)
	LLMTimeout                     time.Duration
	LLMRPM                         int // 0 = unlimited

	Workers    int
	JobTimeout time.Duration
	CacheMB    int
	SyncEvery  time.Duration // how often the remotes are checked for new commits; 0 = never

	MaxRepoMB  int // the size the git server reports, checked on import, and what one download may write
	MaxFiles   int
	MaxFileKB  int
	MaxIndexMB int

	WikiLanguage string
	WikiParallel int    // pages planned or written at a time
	WikiMaxPages int    // the most pages a wiki gets
	GitProtocols string // GIT_ALLOW_PROTOCOL: "http:https" in production

	AdminPassword string        // for the first administrator; "" generates one
	SecretKey     string        // seals git server tokens; "" uses <DataDir>/secret.key
	SessionTTL    time.Duration // how long a sign-in lasts

	// TrustedProxies are the addresses or CIDRs of reverse proxies whose
	// X-Forwarded-For names the client; none: the peer is the client.
	TrustedProxies []string
}

// Room is the tokens the chat model's context keeps for an answer, as the
// chat provider Providers makes keeps them (ai.OpenAI.Room).
func (c Config) Room() int { return (&ai.OpenAI{MaxTokens: c.MaxOutputTokens}).Room() }
