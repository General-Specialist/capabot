package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for Capabot.
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

// DatabaseConfig holds SQLite storage settings.
type DatabaseConfig struct {
	Dir string `yaml:"dir"`
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
	GuildID string `yaml:"guild_id"` // optional: restrict to one guild
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

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config file: %w", err)
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
	if v := os.Getenv("CAPABOT_SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("CAPABOT_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("CAPABOT_DB_DIR"); v != "" {
		cfg.Database.Dir = v
	}
	if v := os.Getenv("CAPABOT_ANTHROPIC_API_KEY"); v != "" {
		cfg.Providers.Anthropic.APIKey = v
	}
	if v := os.Getenv("CAPABOT_ANTHROPIC_MODEL"); v != "" {
		cfg.Providers.Anthropic.Model = v
	}
	if v := os.Getenv("CAPABOT_OPENAI_API_KEY"); v != "" {
		cfg.Providers.OpenAI.APIKey = v
	}
	if v := os.Getenv("CAPABOT_OPENAI_BASE_URL"); v != "" {
		cfg.Providers.OpenAI.BaseURL = v
	}
	if v := os.Getenv("CAPABOT_GEMINI_API_KEY"); v != "" {
		cfg.Providers.Gemini.APIKey = v
	}
	if v := os.Getenv("CAPABOT_GEMINI_MODEL"); v != "" {
		cfg.Providers.Gemini.Model = v
	}
	if v := os.Getenv("CAPABOT_OPENROUTER_API_KEY"); v != "" {
		cfg.Providers.OpenRouter.APIKey = v
	}
	if v := os.Getenv("CAPABOT_OPENROUTER_MODEL"); v != "" {
		cfg.Providers.OpenRouter.Model = v
	}
	if v := os.Getenv("CAPABOT_API_KEY"); v != "" {
		cfg.Security.APIKey = v
	}
	if v := os.Getenv("CAPABOT_TELEGRAM_TOKEN"); v != "" {
		cfg.Transports.Telegram.Token = v
	}
	if v := os.Getenv("CAPABOT_DISCORD_TOKEN"); v != "" {
		cfg.Transports.Discord.Token = v
	}
	if v := os.Getenv("CAPABOT_SLACK_APP_TOKEN"); v != "" {
		cfg.Transports.Slack.AppToken = v
	}
	if v := os.Getenv("CAPABOT_SLACK_BOT_TOKEN"); v != "" {
		cfg.Transports.Slack.BotToken = v
	}
	return cfg
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

	if cfg.Agent.MaxIterations < 1 {
		return fmt.Errorf("agent.max_iterations must be >= 1 (got %d)", cfg.Agent.MaxIterations)
	}

	if cfg.Agent.ContextBudgetPct <= 0 || cfg.Agent.ContextBudgetPct > 1.0 {
		return fmt.Errorf("agent.context_budget_pct must be in (0, 1.0] (got %f)", cfg.Agent.ContextBudgetPct)
	}

	if cfg.Agent.MaxToolOutputTokens < 1 {
		return fmt.Errorf("agent.max_tool_output_tokens must be >= 1 (got %d)", cfg.Agent.MaxToolOutputTokens)
	}

	return nil
}
