// Package app is the composition root: it builds every component from a
// Config and runs the event-driven background pipeline beside the HTTP API.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/account"
	accountstore "deepcode/internal/access/account/store"
	"deepcode/internal/access/role"
	rolestore "deepcode/internal/access/role/store"
	"deepcode/internal/access/session"
	sessionstore "deepcode/internal/access/session/store"
	"deepcode/internal/app/importer"
	"deepcode/internal/app/server"
	"deepcode/internal/app/server/adminapi"
	"deepcode/internal/app/server/authapi"
	"deepcode/internal/app/server/repoapi"
	"deepcode/internal/knowledge/chat"
	chatstore "deepcode/internal/knowledge/chat/store"
	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/knowledge/wiki"
	wikistore "deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/platform/ai"
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
	graphstore "deepcode/internal/repo/index/graph/store"
	"deepcode/internal/repo/index/indexer"
	indexstore "deepcode/internal/repo/index/indexer/store"
	"deepcode/internal/repo/index/vectorstore"
)

type App struct {
	Handler  http.Handler // API and UI
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
	chatP, embedP := providers(cfg)
	mem := cache.New(cfg.CacheMB << 20)
	vectors := &vectorstore.SQLite{DB: db}
	graphs := &graph.Graph{Store: &graphstore.Store{DB: db}}
	retriever := &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: graphs, Embed: embedP, Vectors: vectors, Cache: mem}
	gitc := &git.Client{Dir: repos, AllowProtocol: cfg.GitProtocols, MaxBytes: int64(cfg.MaxRepoMB) << 20}
	servers := &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}
	queue := jobs.New(&jobstore.Store{DB: db}, bus, cfg.JobTimeout)
	cat := &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: queue, Git: gitc, Servers: servers, Cache: mem,
		MaxRepoKB: int64(cfg.MaxRepoMB) << 10, ChatModel: cfg.ChatModel, EmbedModel: cfg.EmbedModel}
	conversations := &chat.Service{Store: &chatstore.Store{DB: db}, Catalog: cat, Retriever: retriever, Chat: chatP}
	wikis := &wikistore.Store{DB: db}
	accounts := &account.Service{Store: &accountstore.Store{DB: db}}
	roles := &role.Service{Store: &rolestore.Store{DB: db}}
	sessions := session.New(&sessionstore.Store{DB: db}, accounts, roles, cfg.SessionTTL)

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
		&authapi.API{Sessions: sessions},
		&repoapi.API{Sessions: sessions, Catalog: cat, Importer: &importer.Service{Catalog: cat}, Servers: servers, Queue: queue,
			Retriever: retriever, Chat: conversations, Wiki: wiki.Reader{Store: wikis, Graph: graphs}},
		&adminapi.API{Sessions: sessions, Accounts: accounts, Roles: roles, Servers: servers, Catalog: cat, Queue: queue, Bus: bus},
		gin.H{"status": "ok", "chatModel": cfg.ChatModel, "embeddingModel": cfg.EmbedModel},
		ui, loopback, cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("trusted proxies (DEEPCODE_TRUSTED_PROXIES): %w", err)
	}
	// Last: a setting that stops the start must not leave a new administrator behind.
	if err := bootstrapAdmin(ctx, accounts, conversations, cfg.AdminPassword); err != nil {
		return nil, err
	}
	return &App{Handler: handler, bus: bus, queue: queue, workers: cfg.Workers, pipeline: p}, nil
}

// Run works the background pipeline until ctx is cancelled: it settles the
// jobs a previous process left running, subscribes the workers and the
// pipeline's reactions, fills in the chunk terms and the import graph an
// older version left out, polls the remotes when SyncEvery is set, and runs
// the event bus.
func (a *App) Run(ctx context.Context) error {
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

// providers builds the configured model clients; nil means turned off.
func providers(cfg Config) (chatP, embedP *ai.OpenAI) {
	if cfg.ChatModel != "" {
		chatP = &ai.OpenAI{BaseURL: cfg.ChatURL, APIKey: cfg.ChatKey, ModelName: cfg.ChatModel,
			MaxTokens: cfg.MaxOutputTokens, ReasoningEffort: cfg.ReasoningEffort, Timeout: cfg.LLMTimeout, RPM: cfg.LLMRPM}
	}
	if cfg.EmbedModel != "" {
		embedP = &ai.OpenAI{BaseURL: cfg.EmbedURL, APIKey: cfg.EmbedKey, ModelName: cfg.EmbedModel,
			Timeout: cfg.LLMTimeout, RPM: cfg.LLMRPM}
	}
	return chatP, embedP
}
