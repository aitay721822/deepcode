// Package app is the composition root: it builds every component from a
// Config and runs the event-driven background pipeline beside the HTTP API.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/account"
	accountstore "deepcode/internal/access/account/store"
	"deepcode/internal/access/oauth"
	oauthstore "deepcode/internal/access/oauth/store"
	"deepcode/internal/access/role"
	rolestore "deepcode/internal/access/role/store"
	"deepcode/internal/access/session"
	sessionstore "deepcode/internal/access/session/store"
	"deepcode/internal/access/token"
	tokenstore "deepcode/internal/access/token/store"
	"deepcode/internal/app/importer"
	"deepcode/internal/app/server"
	"deepcode/internal/app/server/adminapi"
	"deepcode/internal/app/server/authapi"
	"deepcode/internal/app/server/repoapi"
	"deepcode/internal/app/server/repoapi/hooks"
	"deepcode/internal/knowledge/chat"
	chatstore "deepcode/internal/knowledge/chat/store"
	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/knowledge/wiki"
	wikistore "deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/knowledge/wiki/view"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/acp"
	"deepcode/internal/platform/ai/compat"
	"deepcode/internal/platform/ai/tokens"
	"deepcode/internal/platform/ai/usage"
	usagestore "deepcode/internal/platform/ai/usage/store"
	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/repo/catalog"
	catalogstore "deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	serverstore "deepcode/internal/repo/gitserver/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graphstore"
	"deepcode/internal/repo/index/indexer"
	indexstore "deepcode/internal/repo/index/indexer/store"
	"deepcode/internal/repo/index/indexer/vectorstore"
)

type App struct {
	Handler  http.Handler // API and UI
	chat     ai.Chat      // nil when none is configured
	bus      *events.Bus
	queue    *jobs.Queue
	workers  int
	pipeline pipeline
}

// New wires the application around db and creates the first administrator
// when there are no users. ui serves every non-API path; loopback makes the
// API reject foreign Host headers.
func New(ctx context.Context, cfg Config, db *sql.DB, ui http.Handler, loopback bool) (*App, error) {
	repos := filepath.Join(cfg.DataDir, "repos")
	if err := os.MkdirAll(repos, 0o700); err != nil {
		return nil, err
	}
	box, err := gitserver.OpenBox(cfg.DataDir, cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	bus, err := events.New()
	if err != nil {
		return nil, err
	}
	meter := &usage.Manager{Store: &usagestore.Store{DB: db}} // every model request, for the admin console
	var counter *tokens.Counter
	var count func(string) int // estimates the tokens of a request the server counted none for
	if cfg.HasChat() {
		if counter, err = tokens.Load(cfg.ChatTokenizer); err != nil {
			return nil, fmt.Errorf("chat tokenizer (DEEPCODE_CHAT_TOKENIZER): %w", err)
		}
		log.Printf("chat: the model's tokens are counted by %s", counter.Name) // o200k_base: set chat_tokenizer for a model with its own
		count = counter.Count
	}
	chatP, embedP := Providers(cfg, meter, count)
	chatName := "" // no chat model: no wiki
	if chatP != nil {
		chatName = chatP.Name()
	}
	mem := cache.New(cfg.CacheMB << 20)
	vectors := &vectorstore.SQLite{DB: db}
	graphs := &graph.Graph{Store: &graphstore.Store{DB: db}}
	retriever := &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: graphs, Embed: embedP, Vectors: vectors, Cache: mem}
	gitc := &git.Client{Dir: repos, AllowProtocol: cfg.GitProtocols, MaxBytes: int64(cfg.MaxRepoMB) << 20}
	servers := &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}
	queue := jobs.New(&jobstore.Store{DB: db}, bus, cfg.JobTimeout)
	cat := &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: queue, Git: gitc, Servers: servers, Cache: mem,
		MaxRepoKB: int64(cfg.MaxRepoMB) << 10, ChatModel: chatName, EmbedModel: cfg.EmbedModel}
	conversations := &chat.Service{Store: &chatstore.Store{DB: db}, Catalog: cat, Retriever: retriever, Chat: chatP, Tokens: counter}
	wikis := &wikistore.Store{DB: db}
	accounts := &account.Service{Store: &accountstore.Store{DB: db}}
	roles := &role.Service{Store: &rolestore.Store{DB: db}}
	sessions := session.New(&sessionstore.Store{DB: db}, accounts, roles, cfg.SessionTTL)
	apiTokens := &token.Service{Store: &tokenstore.Store{DB: db}, Roles: roles}

	p := pipeline{queue: queue, catalog: cat, syncEvery: cfg.SyncEvery, indexer: &indexer.Indexer{
		Store: &indexstore.Store{DB: db}, Graph: graphs,
		Git: gitc, Servers: servers, Embed: embedP, Vectors: vectors, Events: bus,
		Limits: indexer.Limits{
			MaxFiles:      cfg.MaxFiles,
			MaxFileBytes:  int64(cfg.MaxFileKB) << 10,
			MaxTotalBytes: int64(cfg.MaxIndexMB) << 20,
		},
	}}
	if chatP != nil {
		p.wiki = &wiki.Generator{Store: wikis, Graph: graphs, Catalog: cat, Retriever: retriever, Chat: chatP, Language: cfg.WikiLanguage,
			Parallel: cfg.WikiParallel, MaxPages: cfg.WikiMaxPages}
	}
	handler, err := server.Router(
		&authapi.API{Sessions: sessions, Tokens: apiTokens, OAuth: &oauth.Service{Store: &oauthstore.Store{DB: db}, Tokens: apiTokens}},
		&repoapi.API{Sessions: sessions, Tokens: apiTokens, Catalog: cat, Importer: &importer.Service{Catalog: cat}, Servers: servers, Queue: queue,
			Retriever: retriever, Chat: conversations, Wiki: view.Reader{Store: wikis, Graph: graphs},
			Webhooks: &hooks.API{Catalog: cat, Box: box}},
		&adminapi.API{Sessions: sessions, Accounts: accounts, Roles: roles, Servers: servers, Catalog: cat, Queue: queue, Bus: bus, Usage: meter},
		gin.H{"status": "ok", "chatModel": chatName, "embeddingModel": cfg.EmbedModel},
		ui, loopback, cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("trusted proxies (DEEPCODE_TRUSTED_PROXIES): %w", err)
	}
	// Last: a setting that stops the start must not leave a new administrator behind.
	if err := bootstrapAdmin(ctx, accounts, conversations, cfg.AdminPassword); err != nil {
		return nil, err
	}
	return &App{Handler: handler, chat: chatP, bus: bus, queue: queue, workers: cfg.Workers, pipeline: p}, nil
}

// Run works the background pipeline until ctx is cancelled: it settles the
// jobs a previous process left running, subscribes the workers and the
// pipeline's reactions, fills in the chunk terms and the import graph an
// older version left out, polls the remotes when SyncEvery is set, and runs
// the event bus; an agent's process ends with it.
func (a *App) Run(ctx context.Context) error {
	if agent, ok := a.chat.(*acp.Agent); ok {
		defer agent.Close()
	}
	if err := a.queue.Recover(ctx); err != nil {
		return err
	}
	a.queue.Register(ctx, a.workers, a.pipeline.handlers())
	a.pipeline.subscribe(a.bus)
	go a.queue.Sweep(ctx)
	go a.pipeline.fillTerms(ctx)
	go a.pipeline.linkStale(ctx)
	go a.pipeline.poll(ctx)
	return a.bus.Run(ctx)
}

// ChatName is the chat model's name (ai.Chat Name), an agent's never its
// command line, which may hold a key; "" when none is configured.
func (a *App) ChatName() string {
	if a.chat == nil {
		return ""
	}
	return a.chat.Name()
}

// Providers builds the configured models, each telling meter its requests'
// usage, the chat model's answers the server counted no tokens for estimated
// by count (nil estimates none); nil means turned off. The chat model is an
// agent's (ChatProvider acp), working in <DataDir>/acp, whose process the
// caller ends with its Close (App.Run does), or an OpenAI-compatible server's.
// The retrieval eval and the bench embed with the same client (test/eval
// Embedder).
func Providers(cfg Config, meter usage.Meter, count func(string) int) (chatP ai.Chat, embedP *compat.Client) {
	switch {
	case cfg.ChatProvider == ProviderACP:
		chatP = &acp.Agent{Command: cfg.ACPCommand, Dir: filepath.Join(cfg.DataDir, "acp"), Sessions: cfg.ACPSessions,
			Model: cfg.ChatModel, Effort: cfg.ReasoningEffort, CodexHome: cfg.ACPCodexHome, Connectors: cfg.ACPConnectors, UserConfig: cfg.ACPUserConfig,
			Context: cfg.ChatContext, CompactRatio: cfg.CompactRatio, Timeout: cfg.LLMTimeout, RPM: cfg.LLMRPM, Meter: meter, Count: count}
	case cfg.ChatModel != "":
		chatP = &compat.Client{BaseURL: cfg.ChatURL, APIKey: cfg.ChatKey, ModelName: cfg.ChatModel, API: cfg.ChatAPI, Schema: cfg.ChatSchema,
			MaxTokens: cfg.MaxOutputTokens, ReasoningEffort: cfg.ReasoningEffort, Temperature: cfg.Temperature, Timeout: cfg.LLMTimeout, RPM: cfg.LLMRPM,
			Context: cfg.ChatContext, CompactRatio: cfg.CompactRatio, Meter: meter, Count: count}
	}
	if cfg.EmbedModel != "" {
		embedP = &compat.Client{BaseURL: cfg.EmbedURL, APIKey: cfg.EmbedKey, ModelName: cfg.EmbedModel,
			Timeout: cfg.LLMTimeout, RPM: cfg.LLMRPM, Meter: meter}
	}
	return chatP, embedP
}
