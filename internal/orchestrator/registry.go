package orchestrator

import (
	"fmt"
	"sort"
	"sync"
)

// AgentConfig defines a named agent's configuration.
type AgentConfig struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	SystemPrompt string   `yaml:"system_prompt"`
	Provider     string   `yaml:"provider"`    // "anthropic", "openai", "gemini"
	Model        string   `yaml:"model"`       // optional model override
	Skills       []string `yaml:"skills"`      // skill names to activate
	Tools        []string `yaml:"tools"`       // tool names to enable (nil/empty = all)
	MaxTokens    int      `yaml:"max_tokens"`
	Temperature  float64  `yaml:"temperature"` // 0 = use provider default
}

// Registry holds named agent configurations keyed by ID.
// It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	configs map[string]AgentConfig
}

// NewRegistry creates an empty agent config registry.
func NewRegistry() *Registry {
	return &Registry{
		configs: make(map[string]AgentConfig),
	}
}

// Register adds an AgentConfig to the registry.
// Returns an error if the ID is empty or already registered.
func (r *Registry) Register(cfg AgentConfig) error {
	if cfg.ID == "" {
		return fmt.Errorf("orchestrator: agent ID must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.configs[cfg.ID]; exists {
		return fmt.Errorf("orchestrator: agent %q already registered", cfg.ID)
	}

	r.configs[cfg.ID] = cfg
	return nil
}

// Get retrieves an AgentConfig by ID. Returns the config and true if found,
// zero value and false otherwise.
func (r *Registry) Get(id string) (AgentConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cfg, ok := r.configs[id]
	return cfg, ok
}

// List returns all registered AgentConfigs sorted by ID.
func (r *Registry) List() []AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]AgentConfig, 0, len(r.configs))
	for _, cfg := range r.configs {
		out = append(out, cfg)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})

	return out
}

// Len returns the number of registered agent configurations.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.configs)
}
