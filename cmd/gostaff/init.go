package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/config"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/memory"
	gosdk "github.com/polymath/gostaff/internal/sdk"
	"github.com/polymath/gostaff/internal/skill"
	"github.com/polymath/gostaff/internal/tools"
	"github.com/rs/zerolog"
)

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
func registerSDKPlugins(skillReg *skill.Registry, toolReg *agent.Registry, router *llm.Router, store *memory.Store, logger zerolog.Logger) *gosdk.Registration {
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

		// Persist channel configs declared by the plugin.
		if store != nil {
			for _, ch := range reg.Channels {
				cfg := memory.ChannelConfig{
					ChannelID:      ch.ID,
					Tag:            ch.Tag,
					SystemPrompt:   ch.SystemPrompt,
					SkillNames:     ch.SkillNames,
					Model:          ch.Model,
					MemoryIsolated: ch.MemoryIsolated,
				}
				if err := store.SetChannelConfig(context.Background(), cfg); err != nil {
					logger.Error().Err(err).Str("channel", ch.ID).Msg("failed to persist channel config from plugin")
				} else {
					logger.Info().Str("channel", ch.ID).Msg("plugin channel config persisted")
				}
			}
		}
		combined.Channels = append(combined.Channels, reg.Channels...)
		combined.MemoryPromptBuilders = append(combined.MemoryPromptBuilders, reg.MemoryPromptBuilders...)
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
