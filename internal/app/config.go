package app

import (
	"cmp"
	"time"

	"deepcode/internal/platform/ai"
)

// Config is everything the application needs; cmd/deepcode/config fills it
// from the config file and DEEPCODE_* variables, and flags override that.
type Config struct {
	DataDir string // holds deepcode.db, secret.key and repos/ (the git fetch cache)

	ChatProvider                   string   // "openai", or ProviderACP: how the chat model is reached
	ChatURL, ChatKey, ChatModel    string   // ChatModel "" turns chat and the wiki off, but for an agent, whose model option it sets
	ChatAPI                        string   // compat.Completions or compat.Responses: how the chat model is asked
	ChatSchema                     string   // ai.SchemaServer or ai.SchemaPrompt: where a request for JSON gives its schema
	EmbedURL, EmbedKey, EmbedModel string   // EmbedModel "" turns vector search off
	ReasoningEffort                string   // sent as reasoning_effort when set; an agent's thought_level option
	Temperature                    *float64 // sent as temperature when set
	MaxOutputTokens                int      // 0 leaves it to the server
	ChatContext                    int      // the chat model's context in tokens; 0: unknown (ai.Window)
	CompactRatio                   float64  // the share of it a request fills before it is made smaller
	ChatTokenizer                  string   // the chat model's tokenizer.json; "" counts its tokens by o200k_base (tokens.Load)
	LLMTimeout                     time.Duration
	LLMRPM                         int // 0 = unlimited

	ACPCommand    string // the agent's command line
	ACPSessions   int    // the agent's prompts at a time
	ACPCodexHome  string // CODEX_HOME for the agent; "" leaves Codex its own
	ACPConnectors bool   // Claude: the account's claude.ai connectors join each session
	ACPUserConfig bool   // the user's own agent settings, instructions, MCP servers, plugins and skills join

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

// ProviderACP is the ChatProvider of an agent that speaks the Agent Client
// Protocol (package acp).
const ProviderACP = "acp"

// HasChat reports whether a chat model is configured: an agent, or a model
// of an OpenAI-compatible server.
func (c Config) HasChat() bool { return c.ChatProvider == ProviderACP || c.ChatModel != "" }

// Room is the tokens the chat model's context keeps for an answer, as the
// chat provider Providers makes keeps them (ai.Window.Room).
func (c Config) Room() int { return cmp.Or(c.MaxOutputTokens, ai.DefaultOutput) }
