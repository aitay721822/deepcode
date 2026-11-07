package app

import "time"

// Config is everything the application needs; cmd/deepcode/config fills it
// from the config file and DEEPCODE_* variables, and flags override that.
type Config struct {
	DataDir string // holds deepcode.db, secret.key and repos/ (the git fetch cache)

	ChatURL, ChatKey, ChatModel    string // ChatModel "" turns chat and the wiki off
	EmbedURL, EmbedKey, EmbedModel string // EmbedModel "" turns vector search off
	ReasoningEffort                string // sent as reasoning_effort when set
	MaxOutputTokens                int    // 0 leaves it to the server
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
	GitProtocols string // GIT_ALLOW_PROTOCOL: "http:https" in production

	AdminPassword string        // for the first administrator; "" generates one
	SecretKey     string        // seals git server tokens; "" uses <DataDir>/secret.key
	SessionTTL    time.Duration // how long a sign-in lasts

	// TrustedProxies are the addresses or CIDRs of reverse proxies whose
	// X-Forwarded-For names the client; none: the peer is the client.
	TrustedProxies []string
}
