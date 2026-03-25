package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for GoStaff.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	LogLevel   string           `yaml:"log_level"`
	Database   DatabaseConfig   `yaml:"database"`
	Providers  ProvidersConfig  `yaml:"providers"`
	Agent      AgentConfig      `yaml:"agent"`
	Skills     SkillsConfig     `yaml:"skills"`
	Security   SecurityConfig   `yaml:"security"`
	Transports TransportsConfig `yaml:"transports"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr string `yaml:"addr"`
}

// DatabaseConfig holds Postgres connection settings.
type DatabaseConfig struct {
	URL string `yaml:"url"`
}

// ProvidersConfig holds LLM provider configurations.
type ProvidersConfig struct {
	Anthropic  AnthropicConfig  `yaml:"anthropic"`
	OpenAI     OpenAIConfig     `yaml:"openai"`
	Gemini     GeminiConfig     `yaml:"gemini"`
	OpenRouter OpenRouterConfig `yaml:"openrouter"`
}

// OpenRouterConfig holds OpenRouter gateway settings.
type OpenRouterConfig struct {
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
	AppName string `yaml:"app_name"`
	SiteURL string `yaml:"site_url"`
}

// GeminiConfig holds Google Gemini API settings.
type GeminiConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// AnthropicConfig holds Anthropic Messages API settings.
type AnthropicConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// OpenAIConfig holds OpenAI-compatible provider settings.
type OpenAIConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}

// AgentConfig holds agent loop settings.
type AgentConfig struct {
	MaxIterations       int     `yaml:"max_iterations"`
	ContextBudgetPct    float64 `yaml:"context_budget_pct"`
	MaxToolOutputTokens int     `yaml:"max_tool_output_tokens"`
}

// SkillsConfig holds skill directory settings.
type SkillsConfig struct {
	Dirs []string `yaml:"dirs"`
}

// SecurityConfig holds security-related settings.
type SecurityConfig struct {
	ShellAllowlist   []string `yaml:"shell_allowlist"`
	DrainTimeout     int      `yaml:"drain_timeout"`
	APIKey           string   `yaml:"api_key"`            // optional bearer token for REST API
	RateLimitRPM     int      `yaml:"rate_limit_rpm"`     // requests per minute per IP (0 = disabled)
	ContentFiltering bool     `yaml:"content_filtering"`  // enable prompt injection detection
	SessionTTLDays   int      `yaml:"session_ttl_days"`   // days before inactive sessions are deleted (0 = never)
}

// TransportsConfig holds settings for bot transport adapters.
type TransportsConfig struct {
	Telegram TelegramTransportConfig `yaml:"telegram"`
	Discord  DiscordTransportConfig  `yaml:"discord"`
	Slack    SlackTransportConfig    `yaml:"slack"`
}

// TelegramTransportConfig holds Telegram bot settings.
type TelegramTransportConfig struct {
	Token       string `yaml:"token"`
	WebhookAddr string `yaml:"webhook_addr"` // if set, use webhook mode; else long-poll
}

// DiscordTransportConfig holds Discord bot settings.
type DiscordTransportConfig struct {
	Token   string `yaml:"token"`
	AppID   string `yaml:"app_id"`    // Discord application ID (for slash commands)
	GuildID string `yaml:"guild_id"`  // optional: restrict to one guild
}

// SlackTransportConfig holds Slack bot settings.
type SlackTransportConfig struct {
	AppToken string `yaml:"app_token"` // xapp- token for Socket Mode
	BotToken string `yaml:"bot_token"` // xoxb- token for sending
}

// LoadFromFile reads a YAML config file, merges with defaults, applies
// environment variable overrides, and validates the result.
func LoadFromFile(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file: %w", err)
	}

	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	cfg = applyEnvOverrides(cfg)

	if err := validate(cfg); err != nil {
		return Config{}, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

// applyEnvOverrides returns a new Config with environment variable values
// taking precedence over file-loaded values.
func applyEnvOverrides(cfg Config) Config {
	if v := os.Getenv("GOSTAFF_SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("GOSTAFF_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("GOSTAFF_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("GOSTAFF_ANTHROPIC_API_KEY"); v != "" {
		cfg.Providers.Anthropic.APIKey = v
	}
	if v := os.Getenv("GOSTAFF_ANTHROPIC_MODEL"); v != "" {
		cfg.Providers.Anthropic.Model = v
	}
	if v := os.Getenv("GOSTAFF_OPENAI_API_KEY"); v != "" {
		cfg.Providers.OpenAI.APIKey = v
	}
	if v := os.Getenv("GOSTAFF_OPENAI_BASE_URL"); v != "" {
		cfg.Providers.OpenAI.BaseURL = v
	}
	for _, envKey := range []string{"GOSTAFF_GEMINI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		if v := os.Getenv(envKey); v != "" {
			cfg.Providers.Gemini.APIKey = v
			break
		}
	}
	if v := os.Getenv("GOSTAFF_GEMINI_MODEL"); v != "" {
		cfg.Providers.Gemini.Model = v
	}
	if v := os.Getenv("GOSTAFF_OPENROUTER_API_KEY"); v != "" {
		cfg.Providers.OpenRouter.APIKey = v
	}
	if v := os.Getenv("GOSTAFF_OPENROUTER_MODEL"); v != "" {
		cfg.Providers.OpenRouter.Model = v
	}
	if v := os.Getenv("GOSTAFF_API_KEY"); v != "" {
		cfg.Security.APIKey = v
	}
	if v := os.Getenv("GOSTAFF_TELEGRAM_TOKEN"); v != "" {
		cfg.Transports.Telegram.Token = v
	}
	if v := os.Getenv("GOSTAFF_DISCORD_TOKEN"); v != "" {
		cfg.Transports.Discord.Token = v
	}
	if v := os.Getenv("GOSTAFF_SLACK_APP_TOKEN"); v != "" {
		cfg.Transports.Slack.AppToken = v
	}
	if v := os.Getenv("GOSTAFF_SLACK_BOT_TOKEN"); v != "" {
		cfg.Transports.Slack.BotToken = v
	}
	return cfg
}

// SetKey updates a single dot-path key in the YAML config file on disk.
// The file is created with 0o600 permissions if it does not exist.
func SetKey(configPath, key, value string) error {
	raw := make(map[string]any)
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config: %w", err)
	}
	if err == nil {
		if err2 := yaml.Unmarshal(data, &raw); err2 != nil {
			return fmt.Errorf("parsing config: %w", err2)
		}
	}
	parts := strings.Split(key, ".")
	setNestedKey(raw, parts, value)
	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	return os.WriteFile(configPath, out, 0o600)
}

func setNestedKey(m map[string]any, parts []string, value string) {
	if len(parts) == 1 {
		m[parts[0]] = value
		return
	}
	child, _ := m[parts[0]]
	childMap, ok := child.(map[string]any)
	if !ok {
		childMap = make(map[string]any)
	}
	setNestedKey(childMap, parts[1:], value)
	m[parts[0]] = childMap
}

var validLogLevels = map[string]bool{
	"trace": true,
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
	"fatal": true,
}

// validate checks that all config values are within acceptable bounds.
func validate(cfg Config) error {
	if !strings.HasPrefix(cfg.Server.Addr, ":") {
		return fmt.Errorf("server.addr must start with ':' (got %q)", cfg.Server.Addr)
	}

	if !validLogLevels[cfg.LogLevel] {
		return fmt.Errorf("log_level must be one of trace/debug/info/warn/error/fatal (got %q)", cfg.LogLevel)
	}

	if cfg.Agent.MaxIterations < 0 {
		return fmt.Errorf("agent.max_iterations must be >= 0 (0 = unlimited, got %d)", cfg.Agent.MaxIterations)
	}

	if cfg.Agent.ContextBudgetPct <= 0 || cfg.Agent.ContextBudgetPct > 1.0 {
		return fmt.Errorf("agent.context_budget_pct must be in (0, 1.0] (got %f)", cfg.Agent.ContextBudgetPct)
	}

	if cfg.Agent.MaxToolOutputTokens < 1 {
		return fmt.Errorf("agent.max_tool_output_tokens must be >= 1 (got %d)", cfg.Agent.MaxToolOutputTokens)
	}

	return nil
}
