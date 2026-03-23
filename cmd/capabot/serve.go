package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/api"
	"github.com/polymath/capabot/internal/config"
	"github.com/polymath/capabot/internal/cron"
	applog "github.com/polymath/capabot/internal/log"
	"github.com/polymath/capabot/internal/llm"
	"github.com/polymath/capabot/internal/memory"
	"github.com/polymath/capabot/internal/skill"
	"github.com/polymath/capabot/internal/tools"
	"github.com/polymath/capabot/internal/transport"
	webui "github.com/polymath/capabot/web"
	"github.com/rs/zerolog"
)

// contentFilterEnabled wraps a filter check for use in handlers.
// When filter is nil, all messages are allowed.
func checkContent(filter *agent.ContentFilter, text string) (bool, string) {
	if filter == nil {
		return true, ""
	}
	res := filter.Check(text)
	return !res.Blocked, res.Reason
}

const shutdownTimeout = time.Second

func runServe(configPath string) error {
	// 1. Load config
	cfg, err := loadOrDefault(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// 2. Setup logger with broadcaster so the web UI can stream logs
	broadcaster := applog.NewBroadcaster()
	logger := applog.NewWithWriter(cfg.LogLevel, true, io.MultiWriter(os.Stderr, broadcaster))
	logger.Info().Str("config", configPath).Msg("capabot serve starting")

	// 3. Signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "shutting down...")
		cancel()
	}()

	// 4. Initialize memory store
	store, pool, err := initStore(ctx, cfg.Database.Dir)
	if err != nil {
		return fmt.Errorf("initializing store: %w", err)
	}
	defer pool.Close()

	// 5. Initialize LLM providers and router
	router, err := initRouter(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initializing LLM providers: %w", err)
	}

	// 6. Initialize built-in tools (pass store for memory tools)
	toolRegistry := initToolRegistry(cfg, store)

	// Log which providers were configured (key presence only, not values)
	logger.Info().
		Bool("anthropic", cfg.Providers.Anthropic.APIKey != "").
		Bool("openai", cfg.Providers.OpenAI.APIKey != "").
		Bool("gemini", cfg.Providers.Gemini.APIKey != "").
		Bool("openrouter", cfg.Providers.OpenRouter.APIKey != "").
		Msg("providers")
	if cfg.Providers.Anthropic.APIKey == "" && cfg.Providers.OpenAI.APIKey == "" &&
		cfg.Providers.Gemini.APIKey == "" && cfg.Providers.OpenRouter.APIKey == "" {
		logger.Warn().Msg("no LLM providers configured — chat will not work. See config.example.yaml")
	}

	// 7. Initialize skill registry
	skillRegistry := initSkillRegistry(cfg)
	logger.Info().Int("skills", skillRegistry.Len()).Msg("skills loaded")

	// 7b. Register Tier 3 WASM skills as callable tools
	registerWASMSkills(ctx, skillRegistry, toolRegistry, logger)

	// 7c. Register Tier 2 native Go skills as callable tools
	registerNativeSkills(ctx, skillRegistry, toolRegistry, logger)

	// 7d. Register the skill_create tool so the agent can create new skills
	_ = toolRegistry.Register(tools.NewSkillCreateTool(defaultSkillsDir(), skillRegistry, toolRegistry))

	// 8. Build default agent runner (shared by transport + API server)
	// Inject all loaded skills into the default system prompt.
	basePrompt := "You are a helpful AI assistant."
	allSkills := skillRegistry.List()
	defaultSystemPrompt := skill.BuildSystemPrompt(basePrompt, allSkills)
	agentCfg := agent.AgentConfig{
		ID:            "default",
		Model:         "",
		SystemPrompt:  defaultSystemPrompt,
		MaxIterations: cfg.Agent.MaxIterations,
		MaxTokens:     4096,
	}
	ctxMgrCfg := agent.ContextConfig{
		ContextWindow:       200000,
		BudgetPct:           cfg.Agent.ContextBudgetPct,
		MaxToolOutputTokens: cfg.Agent.MaxToolOutputTokens,
	}
	runAgent := func(runCtx context.Context, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error) {
		ctxMgr := agent.NewContextManager(ctxMgrCfg)
		a := agent.New(agentCfg, router, toolRegistry, ctxMgr, logger)
		if onEvent != nil {
			a.SetOnEvent(onEvent)
		}
		if store != nil {
			a.SetStore(&storeAdapter{store: store})
		}
		return a.Run(runCtx, sessionID, messages)
	}

	// 8b. Start cron scheduler for automations
	scheduler := cron.NewScheduler(store, skillRegistry, runAgent, logger)
	go scheduler.Start(ctx)

	// 9. API server (web UI + REST endpoints)
	apiAddr := cfg.Server.Addr // e.g. ":8080"
	if apiAddr == "" {
		apiAddr = ":8080"
	}
	apiServer := api.New(api.Config{
		Store:          store,
		SkillReg:       skillRegistry,
		Providers:      router.ProviderMap(),
		ToolReg:        toolRegistry,
		DefaultAgent:   runAgent,
		LogBroadcaster: broadcaster,
		Logger:         logger,
		APIKey:         cfg.Security.APIKey,
		RateLimitRPM:   cfg.Security.RateLimitRPM,
		SkillsDir:      defaultSkillsDir(),
		ClawHubToken:   os.Getenv("CAPABOT_GITHUB_TOKEN"),
		StaticFS:       webui.FS(),
		Scheduler:      scheduler,
		ConfigPath:     configPath,
		Router:         router,
	})
	apiSrv := &http.Server{Addr: apiAddr, Handler: apiServer.Handler()}
	go func() {
		logger.Info().Str("addr", apiAddr).Msg("API server listening")
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("API server error")
		}
	}()
	// Graceful shutdown: stop API server when context is cancelled
	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*shutdownTimeout)
		defer shutCancel()
		if err := apiSrv.Shutdown(shutCtx); err != nil {
			logger.Error().Err(err).Msg("API server shutdown error")
		}
	}()

	// 9b. Content filter (optional)
	var contentFilter *agent.ContentFilter
	if cfg.Security.ContentFiltering {
		contentFilter = agent.NewContentFilter(0)
		logger.Info().Msg("content filtering enabled")
	}

	// 9c. Session TTL cleanup (optional background goroutine)
	if cfg.Security.SessionTTLDays > 0 {
		ttl := time.Duration(cfg.Security.SessionTTLDays) * 24 * time.Hour
		go func() {
			ticker := time.NewTicker(6 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					n, err := store.DeleteOldSessions(ctx, ttl)
					if err != nil {
						logger.Error().Err(err).Msg("session TTL cleanup failed")
					} else if n > 0 {
						logger.Info().Int("deleted", n).Msg("session TTL cleanup")
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// 10. Start bot transports
	messageHandler := makeMessageHandler(runAgent, contentFilter, logger)

	// HTTP bot transport (always started)
	httpTransport := transport.NewHTTPTransport(transport.HTTPConfig{
		Addr: ":8081",
	}, logger)
	httpTransport.OnMessage(messageHandler(httpTransport))
	go func() {
		if err := httpTransport.Start(ctx); err != nil && ctx.Err() == nil {
			logger.Error().Err(err).Msg("HTTP transport error")
		}
	}()

	// Telegram (if configured)
	if cfg.Transports.Telegram.Token != "" {
		tg := transport.NewTelegramTransport(transport.TelegramConfig{
			Token:      cfg.Transports.Telegram.Token,
			WebhookURL: cfg.Transports.Telegram.WebhookAddr,
		}, logger)
		tg.OnMessage(messageHandler(tg))
		go func() {
			logger.Info().Msg("Telegram transport starting")
			if err := tg.Start(ctx); err != nil && ctx.Err() == nil {
				logger.Error().Err(err).Msg("Telegram transport error")
			}
		}()
	}

	// Discord (if configured)
	if cfg.Transports.Discord.Token != "" {
		dc := transport.NewDiscordTransport(transport.DiscordConfig{
			Token: cfg.Transports.Discord.Token,
			AppID: cfg.Transports.Discord.GuildID,
		}, logger)
		dc.OnMessage(messageHandler(dc))
		go func() {
			logger.Info().Msg("Discord transport starting")
			if err := dc.Start(ctx); err != nil && ctx.Err() == nil {
				logger.Error().Err(err).Msg("Discord transport error")
			}
		}()
	}

	// Slack (if configured)
	if cfg.Transports.Slack.AppToken != "" && cfg.Transports.Slack.BotToken != "" {
		sl := transport.NewSlackTransport(transport.SlackConfig{
			AppToken: cfg.Transports.Slack.AppToken,
			BotToken: cfg.Transports.Slack.BotToken,
		}, logger)
		sl.OnMessage(messageHandler(sl))
		go func() {
			logger.Info().Msg("Slack transport starting")
			if err := sl.Start(ctx); err != nil && ctx.Err() == nil {
				logger.Error().Err(err).Msg("Slack transport error")
			}
		}()
	}

	// 11. Block until context cancelled
	logger.Info().Msg("capabot ready")
	<-ctx.Done()
	return nil
}

// makeMessageHandler returns a factory that wires inbound messages to the agent runner.
// Transports don't support streaming, so onEvent is passed as nil.
func makeMessageHandler(
	runAgent func(context.Context, string, []llm.ChatMessage, func(agent.AgentEvent)) (*agent.RunResult, error),
	filter *agent.ContentFilter,
	logger zerolog.Logger,
) func(t transport.Transport) func(context.Context, transport.InboundMessage) {
	return func(t transport.Transport) func(context.Context, transport.InboundMessage) {
		return func(msgCtx context.Context, msg transport.InboundMessage) {
			if ok, reason := checkContent(filter, msg.Text); !ok {
				logger.Warn().Str("reason", reason).Str("transport", t.Name()).Msg("message blocked by content filter")
				_ = t.Send(msgCtx, transport.OutboundMessage{
					ChannelID: msg.ChannelID,
					Text:      "Sorry, I can't process that message.",
				})
				return
			}
			messages := []llm.ChatMessage{{Role: "user", Content: msg.Text}}
			result, err := runAgent(msgCtx, msg.ChannelID, messages, nil)
			if err != nil {
				logger.Error().Err(err).Str("session", msg.ChannelID).Str("transport", t.Name()).Msg("agent run failed")
				_ = t.Send(msgCtx, transport.OutboundMessage{
					ChannelID: msg.ChannelID,
					Text:      fmt.Sprintf("error: %v", err),
				})
				return
			}
			_ = t.Send(msgCtx, transport.OutboundMessage{
				ChannelID: msg.ChannelID,
				Text:      result.Response,
			})
		}
	}
}

// initStore opens the SQLite pool, runs migrations, and returns Store + Pool.
func initStore(ctx context.Context, dbDir string) (*memory.Store, *memory.Pool, error) {
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating database directory: %w", err)
	}
	dbPath := filepath.Join(dbDir, "capabot.db")
	pool, err := memory.NewPool(dbPath, 4)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database pool: %w", err)
	}
	if err := memory.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("running migrations: %w", err)
	}
	store := memory.NewStore(pool)
	return store, pool, nil
}

// initRouter creates LLM providers from config and wraps them in a Router.
func initRouter(ctx context.Context, cfg config.Config) (*llm.Router, error) {
	providers := make(map[string]llm.Provider)
	primary := ""

	if cfg.Providers.Anthropic.APIKey != "" {
		p := llm.NewAnthropicProvider(llm.AnthropicConfig{
			APIKey: cfg.Providers.Anthropic.APIKey,
			Model:  cfg.Providers.Anthropic.Model,
		})
		providers["anthropic"] = p
		if primary == "" {
			primary = "anthropic"
		}
	}

	if cfg.Providers.OpenAI.APIKey != "" {
		p := llm.NewOpenAIProvider(llm.OpenAIConfig{
			APIKey:  cfg.Providers.OpenAI.APIKey,
			BaseURL: cfg.Providers.OpenAI.BaseURL,
			Model:   cfg.Providers.OpenAI.Model,
		})
		providers["openai"] = p
		if primary == "" {
			primary = "openai"
		}
	}

	if cfg.Providers.Gemini.APIKey != "" {
		p, err := llm.NewGeminiProvider(ctx, llm.GeminiConfig{
			APIKey: cfg.Providers.Gemini.APIKey,
			Model:  cfg.Providers.Gemini.Model,
		})
		if err != nil {
			return nil, fmt.Errorf("creating gemini provider: %w", err)
		}
		providers["gemini"] = p
		if primary == "" {
			primary = "gemini"
		}
	}

	if cfg.Providers.OpenRouter.APIKey != "" {
		p := llm.NewOpenRouterProvider(llm.OpenRouterConfig{
			APIKey:  cfg.Providers.OpenRouter.APIKey,
			Model:   cfg.Providers.OpenRouter.Model,
			AppName: cfg.Providers.OpenRouter.AppName,
			SiteURL: cfg.Providers.OpenRouter.SiteURL,
		})
		providers["openrouter"] = p
		if primary == "" {
			primary = "openrouter"
		}
	}

	if len(providers) == 0 {
		// No providers configured — create a stub router that will fail at call time.
		// This allows serve to start for health checks etc.
		primary = "anthropic"
	}

	// Build fallbacks list (all configured providers except primary)
	fallbacks := make([]string, 0, len(providers))
	for name := range providers {
		if name != primary {
			fallbacks = append(fallbacks, name)
		}
	}

	router := llm.NewRouter(llm.RouterConfig{
		Primary:   primary,
		Fallbacks: fallbacks,
	}, providers)

	return router, nil
}

// initToolRegistry creates and registers all built-in tools.
func initToolRegistry(cfg config.Config, store *memory.Store) *agent.Registry {
	registry := agent.NewRegistry()

	_ = registry.Register(tools.NewWebSearchTool(tools.WebSearchConfig{}))
	_ = registry.Register(tools.NewWebFetchTool())
	_ = registry.Register(tools.NewFileReadTool(nil))
	_ = registry.Register(tools.NewFileWriteTool(nil))
	_ = registry.Register(tools.NewFileEditTool(nil))
	_ = registry.Register(&tools.GlobTool{})
	_ = registry.Register(&tools.GrepTool{})
	_ = registry.Register(tools.NewShellExecTool(cfg.Security.ShellAllowlist, cfg.Security.DrainTimeout))
	if store != nil {
		_ = registry.Register(tools.NewMemoryStoreTool(store))
		_ = registry.Register(tools.NewMemoryRecallTool(store))
		_ = registry.Register(tools.NewMemoryDeleteTool(store))
	}
	_ = registry.Register(tools.NewScheduleTool(0))
	_ = registry.Register(tools.NewTodoTool())
	_ = registry.Register(&tools.ImageReadTool{})
	_ = registry.Register(tools.NewPDFReadTool())
	_ = registry.Register(&tools.NotebookTool{})

	return registry
}

// initSkillRegistry loads skills from configured directories.
func initSkillRegistry(cfg config.Config) *skill.Registry {
	registry := skill.NewRegistry()

	// Load from config-specified dirs first (highest precedence)
	for _, dir := range cfg.Skills.Dirs {
		registry.LoadDir(dir) //nolint:errcheck
	}

	// Load from standard default dirs
	for _, dir := range skill.DefaultDirs("") {
		registry.LoadDir(dir) //nolint:errcheck
	}

	return registry
}

// wasmAgentTool adapts a skill.WASMTool to the agent.Tool interface.
// It lives in the main package to avoid an import cycle between internal/skill
// and internal/agent.
type wasmAgentTool struct {
	inner *skill.WASMTool
}

func (w *wasmAgentTool) Name() string                { return w.inner.Name() }
func (w *wasmAgentTool) Description() string         { return w.inner.Description() }
func (w *wasmAgentTool) Parameters() json.RawMessage { return w.inner.Parameters() }
func (w *wasmAgentTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	res, err := w.inner.Run(ctx, params)
	return agent.ToolResult{Content: res.Content, IsError: res.IsError}, err
}

// storeAdapter adapts *memory.Store to the agent.StoreWriter interface,
// bridging the type gap without creating an import cycle.
type storeAdapter struct {
	store *memory.Store
}

func (a *storeAdapter) SaveMessage(ctx context.Context, msg agent.StoreMessage) (int64, error) {
	return a.store.SaveMessage(ctx, memory.Message{
		SessionID:  msg.SessionID,
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
		ToolName:   msg.ToolName,
		ToolInput:  msg.ToolInput,
		TokenCount: msg.TokenCount,
	})
}

func (a *storeAdapter) SaveToolExecution(ctx context.Context, exec agent.StoreToolExecution) error {
	return a.store.SaveToolExecution(ctx, memory.ToolExecution{
		SessionID:  exec.SessionID,
		ToolName:   exec.ToolName,
		Input:      exec.Input,
		Output:     exec.Output,
		DurationMs: exec.DurationMs,
		Success:    exec.Success,
	})
}

// nativeAgentTool adapts a skill.NativeTool to the agent.Tool interface.
type nativeAgentTool struct {
	inner *skill.NativeTool
}

func (n *nativeAgentTool) Name() string                { return n.inner.Name() }
func (n *nativeAgentTool) Description() string         { return n.inner.Description() }
func (n *nativeAgentTool) Parameters() json.RawMessage { return n.inner.Parameters() }
func (n *nativeAgentTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	res, err := n.inner.Run(ctx, params)
	return agent.ToolResult{Content: res.Content, IsError: res.IsError}, err
}

// registerNativeSkills compiles and registers all Tier 2 native Go skills found
// in the skill registry into the tool registry.
func registerNativeSkills(ctx context.Context, skillReg *skill.Registry, toolReg *agent.Registry, logger zerolog.Logger) {
	for _, name := range skillReg.NativeSkillNames() {
		skillDir, ok := skillReg.NativePath(name)
		if !ok {
			continue
		}
		parsed := skillReg.Get(name)
		if parsed == nil {
			continue
		}

		exec, err := skill.NewNativeExecutor(ctx, skillDir)
		if err != nil {
			logger.Error().Err(err).Str("skill", name).Str("dir", skillDir).Msg("failed to compile native skill")
			continue
		}

		nativeTool := skill.NewNativeTool(parsed, exec)
		if err := toolReg.Register(&nativeAgentTool{inner: nativeTool}); err != nil {
			logger.Error().Err(err).Str("skill", name).Msg("failed to register native skill tool")
			continue
		}

		logger.Info().Str("skill", name).Str("dir", skillDir).Msg("native skill registered (Tier 2)")
	}
}

// registerWASMSkills compiles and registers all Tier 3 WASM skills found in
// the skill registry into the tool registry. Compilation errors are logged but
// do not prevent other skills from loading.
func registerWASMSkills(ctx context.Context, skillReg *skill.Registry, toolReg *agent.Registry, logger zerolog.Logger) {
	for _, name := range skillReg.WASMSkillNames() {
		wasmPath, ok := skillReg.WASMPath(name)
		if !ok {
			continue
		}
		parsed := skillReg.Get(name)
		if parsed == nil {
			continue
		}

		exec, err := skill.NewWASMExecutorFromFile(ctx, wasmPath)
		if err != nil {
			logger.Error().Err(err).Str("skill", name).Str("wasm", wasmPath).Msg("failed to compile WASM skill")
			continue
		}

		wasmTool := skill.NewWASMTool(parsed, exec)
		if err := toolReg.Register(&wasmAgentTool{inner: wasmTool}); err != nil {
			logger.Error().Err(err).Str("skill", name).Msg("failed to register WASM skill tool")
			exec.Close(ctx) //nolint:errcheck
			continue
		}

		logger.Info().Str("skill", name).Str("wasm", wasmPath).Msg("WASM skill registered (Tier 3)")
	}
}
