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

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/api"
	"github.com/polymath/gostaff/internal/config"
	"github.com/polymath/gostaff/internal/cron"
	applog "github.com/polymath/gostaff/internal/log"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/memory"
	gosdk "github.com/polymath/gostaff/internal/sdk"
	"github.com/polymath/gostaff/internal/skill"
	"github.com/polymath/gostaff/internal/tools"
	"github.com/polymath/gostaff/internal/transport"
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
	sdkRegs := registerSDKPlugins(skillRegistry, toolRegistry, router, logger)

	// 7c. Register Tier 2 native Go skills as callable tools
	registerNativeSkills(ctx, skillRegistry, toolRegistry, logger)

	// 7d. Register skill management tools
	_ = toolRegistry.RegisterExtended(tools.NewSkillCreateMarkdownTool(defaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillEditMarkdownTool(defaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillDeleteMarkdownTool(defaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillCreateTool(defaultSkillsDir(), skillRegistry, toolRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillEditTool(defaultSkillsDir(), skillRegistry))
	_ = toolRegistry.RegisterExtended(tools.NewSkillDeleteTool(defaultSkillsDir(), skillRegistry))
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
	// Inject all loaded skills into the default system prompt.
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
	// Model priority: explicit @tag (already set on cfg.Model) > mode default.
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
		if store != nil {
			a.SetStore(store)
			a.SetUsageOnly(true)
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
	syncDiscordPeopleRoles(ctx, discordRoles, store, logger)

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
		ClawHubToken:    os.Getenv("GOSTAFF_GITHUB_TOKEN"),
		StaticFS:        nil,
		Scheduler:       scheduler,
		ConfigPath:      configPath,
		Router:          router,
		DiscordRoles:    discordRoles,
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
	messageHandler := makeMessageHandler(runAgentEphemeral, store, router, contentFilter, shellTool, logger)

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

// syncDiscordPeopleRoles creates Discord roles for any people or tags that don't have one yet.
func syncDiscordPeopleRoles(ctx context.Context, discordRoles *transport.DiscordRoleClient, store *memory.Store, logger zerolog.Logger) {
	if discordRoles == nil || store == nil {
		return
	}
	people, err := store.ListPeople(ctx)
	if err != nil {
		return
	}

	for _, p := range people {
		if p.DiscordRoleID == "" && p.Username != "" {
			roleID, err := discordRoles.CreateRole(ctx, p.Username)
			if err != nil {
				logger.Warn().Err(err).Str("person", p.Username).Msg("failed to sync Discord role")
			} else {
				_ = store.SetPersonDiscordRoleID(ctx, p.ID, roleID)
				logger.Info().Str("person", p.Username).Str("role_id", roleID).Msg("synced Discord person role")
			}
		}
	}

	allTags := make(map[string]bool)
	for _, p := range people {
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
// If the message starts with @PersonName, the person's prompt is used as the system prompt.
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
	data, err := os.ReadFile(filepath.Join(home, ".gostaff", "avatars", filename))
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

// isApprovalResponse checks if a message is a yes/no response to a pending shell command approval.
func isApprovalResponse(text string) (approved bool, permanent bool, isResponse bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case lower == "yes" || lower == "y" || lower == "allow" || lower == "approve" || lower == "ok" || lower == "sure" || lower == "go ahead":
		return true, false, true
	case strings.HasPrefix(lower, "yes always") || strings.HasPrefix(lower, "always") ||
		strings.HasPrefix(lower, "allow permanently") || strings.HasPrefix(lower, "approve permanently") ||
		strings.HasPrefix(lower, "yes permanently") || lower == "always allow":
		return true, true, true
	case lower == "no" || lower == "n" || lower == "deny" || lower == "cancel" || lower == "nope":
		return false, false, true
	}
	return false, false, false
}

func makeMessageHandler(
	runAgent func(context.Context, string, string, string, []llm.ChatMessage) (*agent.RunResult, error),
	store *memory.Store,
	router *llm.Router,
	filter *agent.ContentFilter,
	shellTool *tools.ShellExecTool,
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

			// Check for pending shell command approval before anything else.
			if shellTool != nil && shellTool.Mode() == tools.ShellModePrompt {
				if pending, ok := shellTool.TakePending(msg.ChannelID); ok {
					approved, permanent, isResponse := isApprovalResponse(msg.Text)
					if isResponse {
						if !approved {
							_ = t.Send(msgCtx, transport.OutboundMessage{
								ChannelID: msg.ChannelID,
								Text:      fmt.Sprintf("Denied `%s`.", pending.Command),
							})
							return
						}
						// Approve and execute.
						if permanent {
							shellTool.ApprovePermanent(msgCtx, pending.Command)
						} else {
							shellTool.ApproveSession(pending.Command)
						}
						output, err := shellTool.RunCommand(msgCtx, pending)
						if err != nil {
							_ = t.Send(msgCtx, transport.OutboundMessage{
								ChannelID: msg.ChannelID,
								Text:      fmt.Sprintf("Error: %v", err),
							})
							return
						}
						scope := "this session"
						if permanent {
							scope = "permanently"
						}
						_ = t.Send(msgCtx, transport.OutboundMessage{
							ChannelID: msg.ChannelID,
							Text:      fmt.Sprintf("Approved `%s` (%s).\n```\n%s\n```", pending.Command, scope, output),
						})
						return
					}
					// Not a yes/no — put the pending command back so it's not lost.
					shellTool.SetPending(msg.ChannelID, pending)
				}
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

			// Detect @PersonName or @tag mention at the start of the message.
			text, people := resolvePeople(msgCtx, store, msg.Text, logger)

			// If no @mention, check for channel binding (auto-route to bound tag or person).
			if len(people) == 0 && store != nil {
				if binding, _ := store.GetChannelBinding(msgCtx, msg.ChannelID); binding != "" {
					if strings.HasPrefix(binding, "persona:") {
						username := strings.TrimPrefix(binding, "persona:")
						if p, err := store.GetPersonByUsername(msgCtx, username); err == nil {
							people = []memory.Person{p}
							logger.Info().Str("channel", msg.ChannelID).Str("person", username).Msg("channel binding auto-routed to person")
						}
					} else {
						if tagged, err := store.GetPeopleByTag(msgCtx, binding); err == nil && len(tagged) > 0 {
							people = tagged
							logger.Info().Str("channel", msg.ChannelID).Str("tag", binding).Int("count", len(tagged)).Msg("channel binding auto-routed to tag")
						}
					}
				}
			}
			messages := []llm.ChatMessage{{Role: "user", Content: text}}

			// Attach channel ID to context so tools (e.g. shell_exec) can key pending state per channel.
			msgCtx = tools.WithSessionID(msgCtx, msg.ChannelID)

			// Load global system prompt (prepended to every person's prompt).
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

			if len(people) == 0 {
				result, err := runAgent(msgCtx, globalSysPrompt, modelID, msg.ChannelID, messages)
				if err != nil {
					logger.Error().Err(err).Str("session", msg.ChannelID).Str("transport", t.Name()).Msg("agent run failed")
					_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", err)})
					return
				}
				sendResponse(result, "", "")
			} else if len(people) == 1 {
				p := people[0]
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
					logger.Error().Err(err).Str("session", msg.ChannelID).Str("person", p.Name).Msg("agent run failed")
					_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", err)})
					return
				}
				sendResponse(result, displayName, avatarToDataURI(p.AvatarURL))
			} else {
				// Multiple people — run in parallel, send as each finishes.
				type personResult struct {
					person memory.Person
					result *agent.RunResult
					err    error
				}
				resultCh := make(chan personResult, len(people))
				for _, p := range people {
					go func(person memory.Person) {
						prompt := person.Prompt
						if globalSysPrompt != "" {
							prompt = globalSysPrompt + "\n\n" + prompt
						}
						res, err := runAgent(msgCtx, prompt, modelID, msg.ChannelID, messages)
						resultCh <- personResult{person: person, result: res, err: err}
					}(p)
				}
				for range people {
					r := <-resultCh
					displayName := r.person.Username
					if displayName == "" {
						displayName = r.person.Name
					}
					if r.err != nil {
						logger.Error().Err(r.err).Str("person", r.person.Name).Msg("person agent failed")
						_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", r.err), DisplayName: displayName, AvatarData: avatarToDataURI(r.person.AvatarURL)})
						continue
					}
					sendResponse(r.result, displayName, avatarToDataURI(r.person.AvatarURL))
				}
			}
		}
	}
}

// handleDefaultRoleCmd processes the /default_role command.
// /default_role @tag      → bind this channel to all people with that tag
// /default_role @person   → bind this channel to a single person
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
			} else if person, err := store.GetPersonByDiscordRoleID(ctx, roleID); err == nil {
				// Person role — bind directly to this person.
				binding := "persona:" + person.Username
				if err := store.SetChannelBinding(ctx, msg.ChannelID, binding); err != nil {
					reply("Failed to set binding.")
					return
				}
				reply(fmt.Sprintf("Default role set to **%s**. All messages in this channel will be answered by this person.", person.Name))
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
			reply(fmt.Sprintf("Default role for this channel: person **%s**", strings.TrimPrefix(binding, "persona:")))
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

	// Try as person username first.
	if person, err := store.GetPersonByUsername(ctx, arg); err == nil {
		if err := store.SetChannelBinding(ctx, msg.ChannelID, "persona:"+person.Username); err != nil {
			reply("Failed to set binding.")
			return
		}
		reply(fmt.Sprintf("Default role set to **%s**. All messages in this channel will be answered by this person.", person.Name))
		return
	}

	// Try as tag.
	tagged, err := store.GetPeopleByTag(ctx, arg)
	if err != nil || len(tagged) == 0 {
		reply(fmt.Sprintf("No person or tag found matching **%s**.", arg))
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
	reply(fmt.Sprintf("Default role set to tag **%s** (%s). All messages in this channel will be answered by these people.", arg, strings.Join(names, ", ")))
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

// resolvePeople checks if text starts with @username, @tag, or a Discord role mention <@&ID>.
// Returns the stripped text and matching people (0 = no match, 1 = direct, N = tag fan-out).
func resolvePeople(ctx context.Context, store *memory.Store, text string, logger zerolog.Logger) (string, []memory.Person) {
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
			// Try person role.
			person, err := store.GetPersonByDiscordRoleID(ctx, roleID)
			if err == nil {
				logger.Info().Str("role_id", roleID).Str("person", person.Username).Msg("Discord role mention resolved")
				return remainder, []memory.Person{person}
			}
			// Try tag role.
			tag, err := store.GetTagByDiscordRoleID(ctx, roleID)
			if err == nil {
				tagged, err := store.GetPeopleByTag(ctx, tag)
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
	person, err := store.GetPersonByUsername(ctx, name)
	if err == nil {
		return remainder, []memory.Person{person}
	}

	// Try as a tag.
	tagged, err := store.GetPeopleByTag(ctx, name)
	if err == nil && len(tagged) > 0 {
		logger.Info().Str("tag", name).Int("count", len(tagged)).Msg("tag matched people")
		return remainder, tagged
	}

	logger.Debug().Str("mention", name).Msg("no person or tag match, treating as plain text")
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
// Returns the registry and the shell_exec tool (for pending-approval handling in transports).
func initToolRegistry(cfg config.Config, store *memory.Store) (*agent.Registry, *tools.ShellExecTool) {
	registry := agent.NewRegistry()

	// Core tools — always sent to the LLM.
	_ = registry.Register(tools.NewFileReadTool(nil))
	_ = registry.Register(tools.NewFileWriteTool(nil))
	_ = registry.Register(tools.NewFileEditTool(nil))
	_ = registry.Register(&tools.SearchTool{})
	shellModeFunc := func() string {
		if store == nil {
			return tools.ShellModeAllowlist
		}
		v, _ := store.GetSetting(context.Background(), "shell_mode")
		if v == "" {
			return tools.ShellModeAllowlist
		}
		return v
	}
	shellTool := tools.NewShellExecTool(cfg.Security.ShellAllowlist, cfg.Security.DrainTimeout, shellModeFunc, store)
	_ = registry.Register(shellTool)
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

	return registry, shellTool
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

// registerSDKPlugins initializes all Go SDK plugins (compiled-in + OpenClaw
// adapters for installed TS/JS/Python plugins) and registers their tools,
// hooks, routes, and providers.
func registerSDKPlugins(skillReg *skill.Registry, toolReg *agent.Registry, router *llm.Router, logger zerolog.Logger) *gosdk.Registration {
	combined := &gosdk.Registration{}

	// Collect all plugins: compiled-in Go plugins + OpenClaw adapters
	plugins := sdkPlugins()
	for _, name := range skillReg.PluginSkillNames() {
		dir, ok := skillReg.PluginPath(name)
		if !ok {
			continue
		}
		plugins = append(plugins, gosdk.NewOpenClawPlugin(dir))
		logger.Info().Str("skill", name).Str("dir", dir).Msg("wrapping OpenClaw plugin via SDK adapter")
	}

	for _, p := range plugins {
		reg, err := gosdk.InitPlugin(p)
		if err != nil {
			logger.Error().Err(err).Msg("failed to init SDK plugin")
			continue
		}

		for _, t := range reg.Tools {
			if err := toolReg.Register(t); err != nil {
				logger.Error().Err(err).Str("tool", t.Name()).Msg("failed to register SDK tool")
				continue
			}
			logger.Info().Str("tool", t.Name()).Msg("SDK plugin tool registered")
		}

		combined.Hooks = append(combined.Hooks, reg.Hooks...)
		combined.Routes = append(combined.Routes, reg.Routes...)

		for _, pe := range reg.Providers {
			router.SetProvider(pe.Name, pe.Provider)
			logger.Info().Str("provider", pe.Name).Msg("SDK plugin provider registered")
		}
	}

	return combined
}

// sdkPlugins returns all compiled-in Go SDK plugins.
// Add new plugins here.
func sdkPlugins() []gosdk.Plugin {
	return []gosdk.Plugin{
		// Add plugins here, e.g.:
		// myplugin.New(),
	}
}
