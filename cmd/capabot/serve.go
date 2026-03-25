package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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

	// 7b. Register Tier 3 plugin skills (TS/JS/Python) as callable tools
	pluginRegs := registerPluginSkills(ctx, skillRegistry, toolRegistry, router, logger)

	// 7c. Register Tier 2 native Go skills as callable tools
	registerNativeSkills(ctx, skillRegistry, toolRegistry, logger)

	// 7d. Register skill_create and skill_edit as extended tools
	_ = toolRegistry.RegisterExtended(tools.NewSkillCreateTool(defaultSkillsDir(), skillRegistry, toolRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillEditTool(defaultSkillsDir(), skillRegistry))

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

				// Register any new plugin skills
				for _, name := range newSkills {
					pluginDir, ok := skillRegistry.PluginPath(name)
					if !ok {
						continue
					}
					proc, err := skill.NewPluginProcess(ctx, pluginDir)
					if err != nil {
						logger.Error().Err(err).Str("skill", name).Msg("hot-reload: failed to start plugin")
						continue
					}
					for _, rt := range proc.Tools() {
						pluginTool := skill.NewPluginTool(rt, proc)
						if err := toolRegistry.Register(&pluginAgentTool{inner: pluginTool}); err != nil {
							logger.Error().Err(err).Str("skill", name).Str("tool", rt.Name).Msg("hot-reload: failed to register tool")
							continue
						}
						logger.Info().Str("skill", name).Str("tool", rt.Name).Msg("hot-reload: plugin tool registered")
					}
					for _, rh := range proc.Hooks() {
						pluginRegs.hooks = append(pluginRegs.hooks, &pluginHook{proc: proc, event: rh.Event})
						logger.Info().Str("skill", name).Str("event", rh.Event).Msg("hot-reload: plugin hook registered")
					}
					for _, rp := range proc.Providers() {
						router.SetProvider(rp.Name, &pluginProvider{proc: proc, name: rp.Name, models: rp.Models})
						logger.Info().Str("skill", name).Str("provider", rp.Name).Msg("hot-reload: plugin provider registered")
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// 8. Build default agent runner (shared by transport + API server)
	// Inject all loaded skills into the default system prompt.
	basePrompt := `You are a helpful AI assistant with access to tools for browsing the web, controlling the desktop, reading/writing files, searching the web, and running shell commands.

Key tools:
- browser: Playwright-based browser automation (headless by default). Handles its own setup — just call it directly, never manually check for playwright or npm first.
- web_search / web_fetch: Search and fetch web pages without opening a browser.
- shell_exec: Run shell commands (allowlisted binaries only).
- file_read / file_write / file_edit / glob / grep: Work with files.

When a tool is available for a task, use it directly. Do not do manual discovery or verification steps before using a tool.`
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

	// getSummarizationModel reads the summarization_model setting.
	getSummarizationModel := func(ctx context.Context) string {
		if store == nil {
			return ""
		}
		v, _ := store.GetSetting(ctx, "summarization_model")
		return v
	}

	// applyMode applies mode settings to an agent config.
	// Model priority: explicit @tag (already set on cfg.Model) > mode default > global default.
	applyMode := func(cfg agent.AgentConfig, ms modeSettings, ctx context.Context) agent.AgentConfig {
		cfg.DisableThinking = ms.DisableThinking
		cfg.Mode = ms.Name
		if cfg.Model == "" && ms.Model != "" {
			cfg.Model = ms.Model
		}
		if cfg.Model == "" && store != nil {
			if v, _ := store.GetSetting(ctx, "default_model"); v != "" {
				cfg.Model = v
			}
		}
		cfg.SummarizationModel = getSummarizationModel(ctx)
		return cfg
	}

	// addPluginHooks attaches any registered plugin hooks to an agent.
	addPluginHooks := func(a *agent.Agent) {
		for _, h := range pluginRegs.hooks {
			a.AddHook(h)
		}
	}

	runAgent := func(runCtx context.Context, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error) {
		ctxMgr := agent.NewContextManager(ctxMgrCfg)
		ms := resolveMode(runCtx)
		cfg := applyMode(agentCfg, ms, runCtx)
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

	// runAgentWithPrompt is like runAgent but with a custom system prompt and optional model override.
	runAgentWithPrompt := func(runCtx context.Context, sysPrompt, model, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error) {
		customCfg := agentCfg
		if sysPrompt != "" {
			customCfg.SystemPrompt = sysPrompt
		}
		if model != "" {
			customCfg.Model = model
		}
		ctxMgr := agent.NewContextManager(ctxMgrCfg)
		ms := resolveMode(runCtx)
		customCfg = applyMode(customCfg, ms, runCtx)
		a := agent.New(customCfg, router, ms.Tools, ctxMgr, logger)
		addPluginHooks(a)
		if onEvent != nil {
			a.SetOnEvent(onEvent)
		}
		if store != nil {
			a.SetStore(store)
		}
		return a.Run(runCtx, sessionID, messages)
	}

	// runAgentEphemeral is like runAgentWithPrompt but doesn't persist messages (for transports).
	runAgentEphemeral := func(runCtx context.Context, sysPrompt, model, sessionID string, messages []llm.ChatMessage) (*agent.RunResult, error) {
		customCfg := agentCfg
		if sysPrompt != "" {
			customCfg.SystemPrompt = sysPrompt
		}
		if model != "" {
			customCfg.Model = model
		}
		ctxMgr := agent.NewContextManager(ctxMgrCfg)
		ms := resolveMode(runCtx)
		customCfg = applyMode(customCfg, ms, runCtx)
		a := agent.New(customCfg, router, ms.Tools, ctxMgr, logger)
		addPluginHooks(a)
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
	syncDiscordRoles(ctx, discordRoles, store, logger)

	apiServer := api.New(api.Config{
		Store:           store,
		SkillReg:        skillRegistry,
		Providers:       router.ProviderMap(),
		ToolReg:         toolRegistry,
		DefaultAgent:    runAgent,
		AgentWithPrompt: runAgentWithPrompt,
		LogBroadcaster:  broadcaster,
		Logger:          logger,
		APIKey:          cfg.Security.APIKey,
		RateLimitRPM:    cfg.Security.RateLimitRPM,
		SkillsDir:       defaultSkillsDir(),
		ClawHubToken:    os.Getenv("CAPABOT_GITHUB_TOKEN"),
		StaticFS:        nil,
		Scheduler:       scheduler,
		ConfigPath:      configPath,
		Router:          router,
		DiscordRoles:    discordRoles,
	})
	// Wire plugin HTTP routes onto a wrapper mux that falls through to the API server.
	apiHandler := apiServer.Handler()
	if len(pluginRegs.routes) > 0 {
		pluginMux := http.NewServeMux()
		for _, pr := range pluginRegs.routes {
			proc := pr.proc
			pluginMux.HandleFunc(pr.method+" "+pr.path, func(w http.ResponseWriter, r *http.Request) {
				headers := make(map[string]string)
				for k := range r.Header {
					headers[k] = r.Header.Get(k)
				}
				query := make(map[string]string)
				for k := range r.URL.Query() {
					query[k] = r.URL.Query().Get(k)
				}
				body, _ := io.ReadAll(r.Body)
				resp, err := proc.InvokeHTTP(r.Context(), skill.HTTPRequest{
					Method:  r.Method,
					Path:    r.URL.Path,
					Headers: headers,
					Query:   query,
					Body:    string(body),
				})
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				w.WriteHeader(resp.Status)
				fmt.Fprint(w, resp.Body)
			})
			logger.Info().Str("method", pr.method).Str("path", pr.path).Msg("plugin HTTP route wired")
		}
		// Fall through to API server for non-plugin routes
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
	messageHandler := makeMessageHandler(runAgentEphemeral, store, router, contentFilter, logger)

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
	logger.Info().Msg("capabot ready")
	<-ctx.Done()
	return nil
}

// syncDiscordRoles creates Discord roles for any personas or tags that don't have one yet.
func syncDiscordRoles(ctx context.Context, discordRoles *transport.DiscordRoleClient, store *memory.Store, logger zerolog.Logger) {
	if discordRoles == nil || store == nil {
		return
	}
	personas, err := store.ListPersonas(ctx)
	if err != nil {
		return
	}

	for _, p := range personas {
		if p.DiscordRoleID == "" && p.Username != "" {
			roleID, err := discordRoles.CreateRole(ctx, p.Username)
			if err != nil {
				logger.Warn().Err(err).Str("persona", p.Username).Msg("failed to sync Discord role")
			} else {
				_ = store.SetPersonaDiscordRoleID(ctx, p.ID, roleID)
				logger.Info().Str("persona", p.Username).Str("role_id", roleID).Msg("synced Discord persona role")
			}
		}
	}

	allTags := make(map[string]bool)
	for _, p := range personas {
		for _, t := range p.Tags {
			allTags[t] = true
		}
	}
	existingTagRoles, _ := store.ListDiscordTagRoles(ctx)
	for tag := range allTags {
		if _, exists := existingTagRoles[tag]; !exists {
			roleID, err := discordRoles.CreateRole(ctx, tag)
			if err != nil {
				logger.Warn().Err(err).Str("tag", tag).Msg("failed to sync Discord tag role")
			} else {
				_ = store.UpsertDiscordTagRole(ctx, tag, roleID)
				logger.Info().Str("tag", tag).Str("role_id", roleID).Msg("synced Discord tag role")
			}
		}
	}
}

// makeMessageHandler returns a factory that wires inbound messages to the agent runner.
// Transports don't support streaming, so onEvent is passed as nil.
// If the message starts with @PersonaName, the persona's prompt is used as the system prompt.
// avatarToDataURI reads a local avatar file (e.g. /api/avatars/abc.png) and returns
// a base64 data URI suitable for Discord's webhook avatar field.
func avatarToDataURI(avatarURL string) string {
	if avatarURL == "" {
		return ""
	}
	// Already a full URL — Discord can fetch it directly, no data URI needed.
	if strings.HasPrefix(avatarURL, "http://") || strings.HasPrefix(avatarURL, "https://") {
		return ""
	}
	// Local path like /api/avatars/abc.png — read from disk.
	filename := strings.TrimPrefix(avatarURL, "/api/avatars/")
	if filename == avatarURL {
		return "" // not an avatar path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".capabot", "avatars", filename))
	if err != nil {
		return ""
	}
	// Detect mime type from extension.
	ext := strings.ToLower(filepath.Ext(filename))
	mime := "image/png"
	switch ext {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".gif":
		mime = "image/gif"
	case ".webp":
		mime = "image/webp"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func makeMessageHandler(
	runAgent func(context.Context, string, string, string, []llm.ChatMessage) (*agent.RunResult, error),
	store *memory.Store,
	router *llm.Router,
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

			// Handle bot commands.
			if strings.HasPrefix(msg.Text, "/default_role") {
				handleDefaultRoleCmd(msgCtx, store, t, msg, logger)
				return
			}
			if strings.HasPrefix(msg.Text, "/chat") || strings.HasPrefix(msg.Text, "/execute") || strings.HasPrefix(msg.Text, "/mode") {
				handleModeCmd(msgCtx, store, t, msg)
				return
			}

			// Extract @model-id tag from the message.
			// Priority: @model-id tag > mode default > global default.
			// Mode and global defaults are applied by applyMode in the agent runner,
			// so we only set modelID here if an explicit @tag was used.
			modelID := extractModelTag(msg.Text, router)
			if modelID != "" {
				msg.Text = strings.Replace(msg.Text, "@"+modelID, "", 1)
				msg.Text = strings.TrimSpace(msg.Text)
			}

			// Detect @PersonaName or @tag mention at the start of the message.
			text, personas := resolvePersonas(msgCtx, store, msg.Text, logger)

			// If no @mention, check for channel binding (auto-route to bound tag or persona).
			if len(personas) == 0 && store != nil {
				if binding, _ := store.GetChannelBinding(msgCtx, msg.ChannelID); binding != "" {
					if strings.HasPrefix(binding, "persona:") {
						username := strings.TrimPrefix(binding, "persona:")
						if p, err := store.GetPersonaByUsername(msgCtx, username); err == nil {
							personas = []memory.Persona{p}
							logger.Info().Str("channel", msg.ChannelID).Str("persona", username).Msg("channel binding auto-routed to persona")
						}
					} else {
						if tagged, err := store.GetPersonasByTag(msgCtx, binding); err == nil && len(tagged) > 0 {
							personas = tagged
							logger.Info().Str("channel", msg.ChannelID).Str("tag", binding).Int("count", len(tagged)).Msg("channel binding auto-routed to tag")
						}
					}
				}
			}
			messages := []llm.ChatMessage{{Role: "user", Content: text}}

			// Load global system prompt (prepended to every persona prompt).
			globalSysPrompt, _ := store.GetSystemPrompt(msgCtx)

			sendResponse := func(result *agent.RunResult, displayName, avatarData string) {
				text := strings.TrimSpace(result.Response)
				if text == "" {
					text = "(empty response)"
					logger.Warn().Str("display_name", displayName).Msg("agent returned empty response")
				}
				if err := t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: text, DisplayName: displayName, AvatarData: avatarData}); err != nil {
					logger.Error().Err(err).Str("display_name", displayName).Msg("transport send failed")
				}
			}

			if len(personas) == 0 {
				result, err := runAgent(msgCtx, globalSysPrompt, modelID, msg.ChannelID, messages)
				if err != nil {
					logger.Error().Err(err).Str("session", msg.ChannelID).Str("transport", t.Name()).Msg("agent run failed")
					_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", err)})
					return
				}
				sendResponse(result, "", "")
			} else if len(personas) == 1 {
				p := personas[0]
				displayName := p.Username
				if displayName == "" {
					displayName = p.Name
				}
				prompt := p.Prompt
				if globalSysPrompt != "" {
					prompt = globalSysPrompt + "\n\n" + prompt
				}
				result, err := runAgent(msgCtx, prompt, modelID, msg.ChannelID, messages)
				if err != nil {
					logger.Error().Err(err).Str("session", msg.ChannelID).Str("persona", p.Name).Msg("agent run failed")
					_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", err)})
					return
				}
				sendResponse(result, displayName, avatarToDataURI(p.AvatarURL))
			} else {
				// Multiple personas — run in parallel, send as each finishes.
				type personaResult struct {
					persona memory.Persona
					result  *agent.RunResult
					err     error
				}
				resultCh := make(chan personaResult, len(personas))
				for _, p := range personas {
					go func(persona memory.Persona) {
						prompt := persona.Prompt
						if globalSysPrompt != "" {
							prompt = globalSysPrompt + "\n\n" + prompt
						}
						res, err := runAgent(msgCtx, prompt, modelID, msg.ChannelID, messages)
						resultCh <- personaResult{persona: persona, result: res, err: err}
					}(p)
				}
				for range personas {
					r := <-resultCh
					displayName := r.persona.Username
					if displayName == "" {
						displayName = r.persona.Name
					}
					if r.err != nil {
						logger.Error().Err(r.err).Str("persona", r.persona.Name).Msg("persona agent failed")
						_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", r.err), DisplayName: displayName, AvatarData: avatarToDataURI(r.persona.AvatarURL)})
						continue
					}
					sendResponse(r.result, displayName, avatarToDataURI(r.persona.AvatarURL))
				}
			}
		}
	}
}

// handleDefaultRoleCmd processes the /default_role command.
// /default_role @tag      → bind this channel to all personas with that tag
// /default_role @persona  → bind this channel to a single persona
// /default_role none      → clear binding
// /default_role           → show current binding
// handleModeCmd processes /chat, /execute, and /mode commands.
func handleModeCmd(ctx context.Context, store *memory.Store, t transport.Transport, msg transport.InboundMessage) {
	reply := func(text string) {
		_ = t.Send(ctx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: text})
	}

	var mode string
	switch {
	case strings.HasPrefix(msg.Text, "/chat"):
		mode = "chat"
	case strings.HasPrefix(msg.Text, "/execute"):
		mode = "execute"
	case strings.HasPrefix(msg.Text, "/mode"):
		arg := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/mode"))
		if arg == "" {
			current := store.GetActiveMode(ctx)
			reply(fmt.Sprintf("Current mode: **%s**", current))
			return
		}
		mode = arg
	}

	if err := store.SetActiveMode(ctx, mode); err != nil {
		reply("Failed to switch mode.")
		return
	}

	desc := ""
	switch mode {
	case "chat":
		desc = " (no tools — faster & cheaper)"
	case "execute":
		desc = " (full tools enabled)"
	}
	reply(fmt.Sprintf("Switched to **%s** mode%s.", mode, desc))
}

func handleDefaultRoleCmd(ctx context.Context, store *memory.Store, t transport.Transport, msg transport.InboundMessage, logger zerolog.Logger) {
	reply := func(text string) {
		_ = t.Send(ctx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: text})
	}

	arg := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/default_role"))

	// Strip Discord role mention format <@&ID>.
	if strings.HasPrefix(arg, "<@&") {
		if end := strings.Index(arg, ">"); end > 0 {
			roleID := arg[3:end]
			// Try tag role first.
			if tag, err := store.GetTagByDiscordRoleID(ctx, roleID); err == nil {
				arg = tag
			} else if persona, err := store.GetPersonaByDiscordRoleID(ctx, roleID); err == nil {
				// Persona role — bind directly to this persona.
				binding := "persona:" + persona.Username
				if err := store.SetChannelBinding(ctx, msg.ChannelID, binding); err != nil {
					reply("Failed to set binding.")
					return
				}
				reply(fmt.Sprintf("Default role set to **%s**. All messages in this channel will be answered by this persona.", persona.Name))
				return
			} else {
				reply("Unknown role.")
				return
			}
		}
	}
	arg = strings.TrimPrefix(arg, "@")

	if arg == "" {
		binding, _ := store.GetChannelBinding(ctx, msg.ChannelID)
		if binding == "" {
			reply("No default role set for this channel.")
		} else if strings.HasPrefix(binding, "persona:") {
			reply(fmt.Sprintf("Default role for this channel: persona **%s**", strings.TrimPrefix(binding, "persona:")))
		} else {
			reply(fmt.Sprintf("Default role for this channel: tag **%s**", binding))
		}
		return
	}

	if arg == "none" || arg == "clear" {
		if err := store.DeleteChannelBinding(ctx, msg.ChannelID); err != nil {
			reply("Failed to clear binding.")
			return
		}
		reply("Default role cleared for this channel.")
		return
	}

	// Try as persona username first.
	if persona, err := store.GetPersonaByUsername(ctx, arg); err == nil {
		if err := store.SetChannelBinding(ctx, msg.ChannelID, "persona:"+persona.Username); err != nil {
			reply("Failed to set binding.")
			return
		}
		reply(fmt.Sprintf("Default role set to **%s**. All messages in this channel will be answered by this persona.", persona.Name))
		return
	}

	// Try as tag.
	tagged, err := store.GetPersonasByTag(ctx, arg)
	if err != nil || len(tagged) == 0 {
		reply(fmt.Sprintf("No persona or tag found matching **%s**.", arg))
		return
	}

	if err := store.SetChannelBinding(ctx, msg.ChannelID, arg); err != nil {
		reply("Failed to set binding.")
		return
	}

	names := make([]string, len(tagged))
	for i, p := range tagged {
		names[i] = p.Name
	}
	reply(fmt.Sprintf("Default role set to tag **%s** (%s). All messages in this channel will be answered by these personas.", arg, strings.Join(names, ", ")))
}

// extractModelTag checks if text contains @model-id matching a known model.
func extractModelTag(text string, router *llm.Router) string {
	if router == nil {
		return ""
	}
	for _, m := range router.Models() {
		if strings.Contains(text, "@"+m.ID) {
			return m.ID
		}
	}
	return ""
}

// resolvePersonas checks if text starts with @username, @tag, or a Discord role mention <@&ID>.
// Returns the stripped text and matching personas (0 = no match, 1 = direct, N = tag fan-out).
func resolvePersonas(ctx context.Context, store *memory.Store, text string, logger zerolog.Logger) (string, []memory.Persona) {
	if store == nil || len(text) < 2 {
		return text, nil
	}

	// Check for Discord role mention: <@&ROLE_ID>
	if strings.HasPrefix(text, "<@&") {
		end := strings.Index(text, ">")
		if end > 3 {
			roleID := text[3:end]
			remainder := strings.TrimLeft(text[end+1:], " ")
			if remainder == "" {
				remainder = text
			}
			// Try persona role.
			persona, err := store.GetPersonaByDiscordRoleID(ctx, roleID)
			if err == nil {
				logger.Info().Str("role_id", roleID).Str("persona", persona.Username).Msg("Discord role mention resolved")
				return remainder, []memory.Persona{persona}
			}
			// Try tag role.
			tag, err := store.GetTagByDiscordRoleID(ctx, roleID)
			if err == nil {
				tagged, err := store.GetPersonasByTag(ctx, tag)
				if err == nil && len(tagged) > 0 {
					logger.Info().Str("role_id", roleID).Str("tag", tag).Int("count", len(tagged)).Msg("Discord tag role mention resolved")
					return remainder, tagged
				}
			}
		}
	}

	if text[0] != '@' {
		return text, nil
	}
	rest := text[1:]
	name := rest
	remainder := ""
	for i, c := range rest {
		if c == ' ' || c == '\n' {
			name = rest[:i]
			remainder = rest[i+1:]
			break
		}
	}
	if name == "" {
		return text, nil
	}
	if remainder == "" {
		remainder = text
	}

	// Try exact username first (the @mention handle).
	persona, err := store.GetPersonaByUsername(ctx, name)
	if err == nil {
		return remainder, []memory.Persona{persona}
	}

	// Try as a tag.
	tagged, err := store.GetPersonasByTag(ctx, name)
	if err == nil && len(tagged) > 0 {
		logger.Info().Str("tag", name).Int("count", len(tagged)).Msg("tag matched personas")
		return remainder, tagged
	}

	logger.Debug().Str("mention", name).Msg("no persona or tag match, treating as plain text")
	return text, nil
}

// initStore opens the Postgres pool, runs migrations, and returns Store + Pool.
func initStore(ctx context.Context, dbURL string) (*memory.Store, *memory.Pool, error) {
	pool, err := memory.NewPool(dbURL)
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

	// Core tools — always sent to the LLM.
	_ = registry.Register(tools.NewFileReadTool(nil))
	_ = registry.Register(tools.NewFileWriteTool(nil))
	_ = registry.Register(tools.NewFileEditTool(nil))
	_ = registry.Register(&tools.SearchTool{})
	_ = registry.Register(tools.NewShellExecTool(cfg.Security.ShellAllowlist, cfg.Security.DrainTimeout))
	_ = registry.Register(tools.NewWebSearchTool(tools.WebSearchConfig{}))
	_ = registry.Register(tools.NewWebFetchTool())

	// Extended tools — accessible via use_tool, not sent individually.
	_ = registry.RegisterExtended(tools.NewBrowserTool(""))
	_ = registry.RegisterExtended(&tools.NotebookTool{})
	_ = registry.RegisterExtended(tools.NewScheduleTool(0))
	_ = registry.RegisterExtended(tools.NewTodoTool())
	if store != nil {
		_ = registry.RegisterExtended(tools.NewMemoryTool(store))
	}

	// Meta-tool that proxies to extended tools (must be registered after them).
	_ = registry.Register(tools.NewUseToolTool(registry))

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

// pluginAgentTool adapts a skill.PluginTool to the agent.Tool interface.
// It lives in the main package to avoid an import cycle between internal/skill
// and internal/agent.
type pluginAgentTool struct {
	inner *skill.PluginTool
}

func (p *pluginAgentTool) Name() string                { return p.inner.Name() }
func (p *pluginAgentTool) Description() string         { return p.inner.Description() }
func (p *pluginAgentTool) Parameters() json.RawMessage { return p.inner.Parameters() }
func (p *pluginAgentTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	res, err := p.inner.Run(ctx, params)
	return agent.ToolResult{Content: res.Content, IsError: res.IsError}, err
}

// pluginHook adapts a skill.PluginProcess hook to the agent.ToolHook interface.
type pluginHook struct {
	proc  *skill.PluginProcess
	event string // "pre_tool_use" or "post_tool_use"
}

func (h *pluginHook) BeforeToolUse(ctx context.Context, toolName string, params json.RawMessage) (bool, json.RawMessage, error) {
	if h.event != "pre_tool_use" {
		return true, nil, nil
	}
	result, err := h.proc.InvokeHook(ctx, "pre_tool_use", toolName, params, nil)
	if err != nil {
		return true, nil, err
	}
	if !result.Allow {
		return false, nil, nil
	}
	return true, result.Params, nil
}

func (h *pluginHook) AfterToolUse(ctx context.Context, toolName string, params json.RawMessage, result json.RawMessage) (json.RawMessage, error) {
	if h.event != "post_tool_use" {
		return nil, nil
	}
	hookResult, err := h.proc.InvokeHook(ctx, "post_tool_use", toolName, params, result)
	if err != nil {
		return nil, err
	}
	return hookResult.Result, nil
}

// pluginProvider adapts a skill.PluginProcess LLM provider to the llm.Provider interface.
type pluginProvider struct {
	proc   *skill.PluginProcess
	name   string
	models json.RawMessage
}

func (p *pluginProvider) Name() string { return p.name }

func (p *pluginProvider) Models() []llm.ModelInfo {
	var ids []string
	_ = json.Unmarshal(p.models, &ids)
	out := make([]llm.ModelInfo, len(ids))
	for i, id := range ids {
		out[i] = llm.ModelInfo{ID: id, Name: id}
	}
	return out
}

func (p *pluginProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	msgsJSON, _ := json.Marshal(req.Messages)
	toolsJSON, _ := json.Marshal(req.Tools)
	resp, err := p.proc.InvokeChat(ctx, skill.ChatRequest{
		Provider: p.name,
		Model:    req.Model,
		Messages: msgsJSON,
		System:   req.System,
		Tools:    toolsJSON,
		MaxTok:   req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("plugin provider %s: %s", p.name, resp.Error)
	}
	return &llm.ChatResponse{
		Content:  resp.Content,
		Model:    resp.Model,
		Provider: p.name,
	}, nil
}

func (p *pluginProvider) Stream(_ context.Context, _ llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("plugin provider %q does not support streaming", p.name)
}

// pluginHTTPRoute holds a plugin route handler for deferred registration on the API mux.
type pluginHTTPRoute struct {
	method string
	path   string
	proc   *skill.PluginProcess
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

// pluginRegistrations holds everything plugins registered during init.
type pluginRegistrations struct {
	hooks  []agent.ToolHook
	routes []pluginHTTPRoute
}

// registerPluginSkills spawns plugin processes and registers all tools, hooks,
// HTTP routes, and LLM providers they provide. A single plugin can register
// multiple of each. Runtime errors are logged but don't prevent other plugins.
func registerPluginSkills(ctx context.Context, skillReg *skill.Registry, toolReg *agent.Registry, router *llm.Router, logger zerolog.Logger) pluginRegistrations {
	var reg pluginRegistrations

	for _, name := range skillReg.PluginSkillNames() {
		pluginDir, ok := skillReg.PluginPath(name)
		if !ok {
			continue
		}

		proc, err := skill.NewPluginProcess(ctx, pluginDir)
		if err != nil {
			logger.Error().Err(err).Str("skill", name).Str("dir", pluginDir).Msg("failed to start plugin")
			continue
		}

		// Register tools
		for _, rt := range proc.Tools() {
			pluginTool := skill.NewPluginTool(rt, proc)
			if err := toolReg.Register(&pluginAgentTool{inner: pluginTool}); err != nil {
				logger.Error().Err(err).Str("skill", name).Str("tool", rt.Name).Msg("failed to register plugin tool")
				continue
			}
			logger.Info().Str("skill", name).Str("tool", rt.Name).Str("runtime", proc.Runtime()).Msg("plugin tool registered")
		}

		// Collect hooks
		for _, rh := range proc.Hooks() {
			reg.hooks = append(reg.hooks, &pluginHook{proc: proc, event: rh.Event})
			logger.Info().Str("skill", name).Str("event", rh.Event).Str("hook", rh.Name).Msg("plugin hook registered")
		}

		// Collect HTTP routes (wired to mux later)
		for _, rr := range proc.Routes() {
			reg.routes = append(reg.routes, pluginHTTPRoute{method: rr.Method, path: rr.Path, proc: proc})
			logger.Info().Str("skill", name).Str("method", rr.Method).Str("path", rr.Path).Msg("plugin route registered")
		}

		// Register LLM providers on the router
		for _, rp := range proc.Providers() {
			router.SetProvider(rp.Name, &pluginProvider{proc: proc, name: rp.Name, models: rp.Models})
			logger.Info().Str("skill", name).Str("provider", rp.Name).Msg("plugin provider registered")
		}

		if len(proc.Tools()) == 0 && len(proc.Hooks()) == 0 && len(proc.Routes()) == 0 && len(proc.Providers()) == 0 {
			logger.Warn().Str("skill", name).Msg("plugin registered nothing")
			proc.Close() //nolint:errcheck
		}
	}

	return reg
}
