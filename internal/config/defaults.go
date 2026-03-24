package config

import (
	"os"
	"path/filepath"
)

// Default returns a Config with sane defaults for zero-config startup.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Addr: ":8080",
		},
		LogLevel: "info",
		Database: DatabaseConfig{
			Dir: defaultDataDir(),
		},
		Providers: ProvidersConfig{
			Anthropic: AnthropicConfig{
				Model: "claude-sonnet-4-6-20250514",
			},
			OpenAI: OpenAIConfig{
				BaseURL: "https://api.openai.com/v1",
			},
			Gemini: GeminiConfig{
				Model: "gemini-3-flash-preview",
			},
		},
		Agent: AgentConfig{
			MaxIterations:      0, // 0 = unlimited
			ContextBudgetPct:   0.8,
			MaxToolOutputTokens: 4096,
		},
		Skills: SkillsConfig{
			Dirs: []string{defaultSkillsDir()},
		},
		Security: SecurityConfig{
			ShellAllowlist: []string{"ls", "cat", "head", "tail", "grep", "wc", "date", "echo", "pwd", "open", "node", "npx"},
			DrainTimeout:   30,
		},
	}
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".capabot", "data")
	}
	return filepath.Join(home, ".capabot", "data")
}

func defaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".capabot", "skills")
	}
	return filepath.Join(home, ".capabot", "skills")
}
