package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/api"
	"github.com/polymath/gostaff/internal/config"
	"github.com/polymath/gostaff/internal/cron"
	applog "github.com/polymath/gostaff/internal/log"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/skill"
	"github.com/polymath/gostaff/internal/tools"
	"github.com/polymath/gostaff/internal/transport"
)

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
	logger.Info().Str("config", configPath).Msg("gostaff serve starting")

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
	store, pool, err := initStore(ctx, cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("initializing store: %w", err)
	}
	defer pool.Close()

	// Clean up any runs left in "running" state from a previous crash/restart.
	if store != nil {
		_ = store.MarkStaleRunsAsFailed(ctx)
	}

	// 5. Initialize LLM providers and router
	router, err := initRouter(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initializing LLM providers: %w", err)
	}

	// 6. Initialize built-in tools (pass store for memory tools)
	toolRegistry, shellTool := initToolRegistry(cfg, store)

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

	// 7b. Register Tier 3 plugins via Go SDK (in-process Go + OpenClaw adapters)
	sdkRegs := registerSDKPlugins(skillRegistry, toolRegistry, router, store, logger)

	// 7c. Register Tier 2 native Go skills as callable tools
	registerNativeSkills(ctx, skillRegistry, toolRegistry, logger)

	// 7d. Register skill management tools
	_ = toolRegistry.RegisterExtended(tools.NewSkillCreateMarkdownTool(config.DefaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillEditMarkdownTool(config.DefaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillDeleteMarkdownTool(config.DefaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillCreateTool(config.DefaultSkillsDir(), skillRegistry, toolRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillEditTool(config.DefaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillDeleteTool(config.DefaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillSearchTool(skill.NewClawHubClient(skill.ClawHubConfig{})))

	// 7e. Hot-reload: poll skill directories every 10s for newly installed skills
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				newSkills := skillRegistry.LoadNewSkills()
				if len(newSkills) == 0 {
					continue
				}
				logger.Info().Strs("skills", newSkills).Msg("hot-reload: new skills detected")
			case <-ctx.Done():
				return
			}
		}
	}()

	// 8. Build default agent runner (shared by transport + API server)
	basePrompt := `You are a helpful AI assistant with access to tools for browsing the web, controlling the desktop, reading/writing files, searching the web, and running shell commands.

Key tools:
- browser: Playwright-based browser automation (headless by default). Handles its own setup — just call it directly, never manually check for playwright or npm first.
- web_search / web_fetch: Search and fetch web pages without opening a browser.
- shell_exec: Run shell commands (allowlisted binaries only).
- file_read / file_write / file_edit / glob / grep: Work with files.

When a tool is available for a task, use it directly. Do not do manual discovery or verification steps before using a tool.

Skills vs plugins:
- skill_create_markdown: creates a SKILL — a markdown file with instructions injected into the agent's context. Use this by default when asked to "make a skill". No code needed.
- plugin_create: creates a PLUGIN — a compiled Go binary the agent can invoke as a tool. Only use this when the task genuinely requires running standalone executable code that cannot be done with existing tools.`
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
	// emptyToolRegistry is used for chat mode (no tools = way fewer tokens).
	emptyToolRegistry := agent.NewRegistry()

	// modeSettings holds resolved per-mode config.
	type modeSettings struct {
		Tools           *agent.Registry
		DisableThinking bool
		Model           string // mode's default model (empty = use global default)
		Name            string // mode name for usage tracking
	}

	// resolveMode returns tool registry, thinking flag, and default model for the active mode.
	resolveMode := func(ctx context.Context) modeSettings {
		if store == nil {
			return modeSettings{Tools: toolRegistry}
		}
		modeName := store.GetActiveMode(ctx)
		ms := modeSettings{Tools: toolRegistry, Name: modeName}
		if modeName == "chat" {
			ms.Tools = emptyToolRegistry
			ms.DisableThinking = true
		}
		// Load mode's default model
		if modeKeys, err := store.GetMode(ctx, modeName); err == nil && modeKeys.Model != "" {
			ms.Model = modeKeys.Model
		}
		return ms
	}

	// applyMode applies mode settings to an agent config.
	applyMode := func(cfg agent.AgentConfig, ms modeSettings, ctx context.Context) agent.AgentConfig {
		cfg.DisableThinking = ms.DisableThinking
		cfg.Mode = ms.Name
		if cfg.Model == "" && ms.Model != "" {
			cfg.Model = ms.Model
		}
		// Load per-mode summarization model.
		if store != nil {
			if modeKeys, err := store.GetMode(ctx, ms.Name); err == nil && modeKeys.SummarizationModel != "" {
				cfg.SummarizationModel = modeKeys.SummarizationModel
			}
		}
		return cfg
	}

	// addPluginHooks attaches any registered SDK plugin hooks to an agent.
	addPluginHooks := func(a *agent.Agent) {
		for _, h := range sdkRegs.Hooks {
			a.AddHook(h)
		}
	}

	runAgent := func(runCtx context.Context, sysPrompt, model, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error) {
		cfg := agentCfg
		if sysPrompt != "" {
			cfg.SystemPrompt = sysPrompt
		}
		if model != "" {
			cfg.Model = model
		}

		// Inject memory prompt sections from plugins.
		for _, mpb := range sdkRegs.MemoryPromptBuilders {
			text, err := mpb.Build(runCtx, sessionID)
			if err != nil {
				logger.Warn().Err(err).Str("section", mpb.Name()).Msg("memory prompt section failed")
				continue
			}
			if text != "" {
				cfg.SystemPrompt += "\n\n" + text
			}
		}

		ctxMgr := agent.NewContextManager(ctxMgrCfg)
		ms := resolveMode(runCtx)
		cfg = applyMode(cfg, ms, runCtx)
		a := agent.New(cfg, router, ms.Tools, ctxMgr, logger)
		addPluginHooks(a)
		if onEvent != nil {
			a.SetOnEvent(onEvent)
		}
		if store != nil {
			a.SetStore(store)
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
	discordRoles := transport.NewDiscordRoleClient(cfg.Transports.Discord.Token, cfg.Transports.Discord.GuildID)
	transport.SyncPeopleRoles(ctx, discordRoles, store, logger)

	apiServer := api.New(api.Config{
		Store:          store,
		SkillReg:       skillRegistry,
		Providers:      router.ProviderMap(),
		ToolReg:        toolRegistry,
		RunAgent:       runAgent,
		LogBroadcaster: broadcaster,
		Logger:         logger,
		APIKey:         cfg.Security.APIKey,
		RateLimitRPM:   cfg.Security.RateLimitRPM,
		SkillsDir:      config.DefaultSkillsDir(),
		ClawHubToken:   os.Getenv("GOSTAFF_GITHUB_TOKEN"),
		StaticFS:       nil,
		Scheduler:      scheduler,
		ConfigPath:     configPath,
		Router:         router,
		DiscordRoles:   discordRoles,
	})
	// Restore execute fallback if previously enabled.
	apiServer.RestoreExecuteFallback(ctx)

	// Wire SDK plugin HTTP routes onto a wrapper mux that falls through to the API server.
	apiHandler := apiServer.Handler()
	if len(sdkRegs.Routes) > 0 {
		pluginMux := http.NewServeMux()
		for _, rt := range sdkRegs.Routes {
			pluginMux.HandleFunc(rt.Method+" "+rt.Path, rt.Handler)
			logger.Info().Str("method", rt.Method).Str("path", rt.Path).Msg("SDK plugin route wired")
		}
		pluginMux.Handle("/", apiHandler)
		apiHandler = pluginMux
	}
	apiSrv := &http.Server{Addr: apiAddr, Handler: apiHandler}
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
	messageHandler := makeMessageHandler(runAgent, store, router, contentFilter, shellTool, logger)

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
			AppID: cfg.Transports.Discord.AppID,
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
	logger.Info().Msg("gostaff ready")
	<-ctx.Done()
	return nil
}
