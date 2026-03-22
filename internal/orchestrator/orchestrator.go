package orchestrator

import (
	"context"
	"fmt"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/llm"
	"github.com/polymath/capabot/internal/skill"
	"github.com/rs/zerolog"
)

// Orchestrator creates and runs agents from registry configurations.
type Orchestrator struct {
	registry  *Registry
	providers map[string]llm.Provider
	tools     *agent.Registry   // global tool registry (all available tools)
	skills    *skill.Registry   // global skill registry
	store     agent.StoreWriter // nil = no persistence
	logger    zerolog.Logger
}

// New creates a new Orchestrator with the given dependencies.
func New(
	reg *Registry,
	providers map[string]llm.Provider,
	tools *agent.Registry,
	skills *skill.Registry,
	store agent.StoreWriter,
	logger zerolog.Logger,
) *Orchestrator {
	return &Orchestrator{
		registry:  reg,
		providers: providers,
		tools:     tools,
		skills:    skills,
		store:     store,
		logger:    logger,
	}
}

// Dispatch routes a message to the named agent and runs the ReAct loop.
// sessionID scopes the conversation history.
func (o *Orchestrator) Dispatch(ctx context.Context, agentID, sessionID string, messages []llm.ChatMessage) (*agent.RunResult, error) {
	cfg, ok := o.registry.Get(agentID)
	if !ok {
		return nil, fmt.Errorf("orchestrator: agent %q not found", agentID)
	}

	a, err := o.buildAgent(cfg, sessionID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: building agent %q: %w", agentID, err)
	}

	// Copy messages to avoid mutating the caller's slice.
	msgsCopy := make([]llm.ChatMessage, len(messages))
	copy(msgsCopy, messages)

	result, err := a.Run(ctx, sessionID, msgsCopy)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: %w", err)
	}

	return result, nil
}

// buildAgent creates a configured agent.Agent from an AgentConfig.
func (o *Orchestrator) buildAgent(cfg AgentConfig, sessionID string) (*agent.Agent, error) {
	// Resolve provider.
	provider, err := o.resolveProvider(cfg)
	if err != nil {
		return nil, err
	}

	// Build filtered tool registry.
	toolReg := o.buildToolRegistry(cfg)

	// Register the SpawnAgentTool so this agent can delegate to peers.
	spawnTool := newSpawnAgentTool(o, sessionID)
	if regErr := toolReg.Register(spawnTool); regErr != nil {
		// Spawn tool already registered — not fatal, log and continue.
		o.logger.Warn().
			Str("agent_id", cfg.ID).
			Err(regErr).
			Msg("spawn_agent tool already registered")
	}

	// Build system prompt with active skill instructions.
	activeSkills := skill.ActiveSkillsFromNames(o.skills, cfg.Skills)
	systemPrompt := skill.BuildSystemPrompt(cfg.SystemPrompt, activeSkills)

	// Build context manager.
	ctxMgr := agent.NewContextManager(agent.ContextConfig{
		ContextWindow:       100000,
		BudgetPct:           0.80,
		MaxToolOutputTokens: 4000,
	})

	// Build temperature pointer only if non-zero.
	var tempPtr *float64
	if cfg.Temperature != 0 {
		t := cfg.Temperature
		tempPtr = &t
	}

	agentCfg := agent.AgentConfig{
		ID:           cfg.ID,
		Model:        cfg.Model,
		SystemPrompt: systemPrompt,
		MaxTokens:    cfg.MaxTokens,
		Temperature:  tempPtr,
	}

	a := agent.New(agentCfg, provider, toolReg, ctxMgr, o.logger)
	if o.store != nil {
		a.SetStore(o.store)
	}

	return a, nil
}

// resolveProvider looks up the provider for the given AgentConfig.
// If the named provider is not found, it falls back to the first available
// provider (logging a warning). Returns an error if no providers are available.
func (o *Orchestrator) resolveProvider(cfg AgentConfig) (llm.Provider, error) {
	if len(o.providers) == 0 {
		return nil, fmt.Errorf("orchestrator: no providers available")
	}

	if p, ok := o.providers[cfg.Provider]; ok {
		return p, nil
	}

	// Fall back to first available provider.
	var first llm.Provider
	for _, p := range o.providers {
		first = p
		break
	}

	o.logger.Warn().
		Str("agent_id", cfg.ID).
		Str("requested_provider", cfg.Provider).
		Str("fallback_provider", first.Name()).
		Msg("provider not found, using fallback")

	return first, nil
}

// buildToolRegistry creates a tool registry for the agent.
// If cfg.Tools is nil/empty, all global tools are included.
// Otherwise only the named tools are included (unknown names are skipped with a warning).
func (o *Orchestrator) buildToolRegistry(cfg AgentConfig) *agent.Registry {
	reg := agent.NewRegistry()

	if len(cfg.Tools) == 0 {
		// Include all globally registered tools.
		for _, t := range o.tools.List() {
			if err := reg.Register(t); err != nil {
				o.logger.Warn().
					Str("tool", t.Name()).
					Err(err).
					Msg("failed to register tool in agent registry")
			}
		}
		return reg
	}

	// Include only the named tools.
	for _, name := range cfg.Tools {
		t := o.tools.Get(name)
		if t == nil {
			o.logger.Warn().
				Str("agent_id", cfg.ID).
				Str("tool", name).
				Msg("tool not found in global registry, skipping")
			continue
		}
		if err := reg.Register(t); err != nil {
			o.logger.Warn().
				Str("tool", name).
				Err(err).
				Msg("failed to register tool in agent registry")
		}
	}

	return reg
}
