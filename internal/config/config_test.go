package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := Default()

	if cfg.Server.Addr != ":8080" {
		t.Errorf("expected default addr :8080, got %s", cfg.Server.Addr)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default log level info, got %s", cfg.LogLevel)
	}
	if cfg.Database.URL == "" {
		t.Error("expected non-empty default database URL")
	}
	if cfg.Agent.MaxIterations != 25 {
		t.Errorf("expected default max iterations 25, got %d", cfg.Agent.MaxIterations)
	}
	if cfg.Agent.ContextBudgetPct != 0.8 {
		t.Errorf("expected default context budget 0.8, got %f", cfg.Agent.ContextBudgetPct)
	}
	if cfg.Agent.MaxToolOutputTokens != 4096 {
		t.Errorf("expected default max tool output 4096, got %d", cfg.Agent.MaxToolOutputTokens)
	}
	if cfg.Skills.Dirs == nil || len(cfg.Skills.Dirs) == 0 {
		t.Error("expected at least one default skill directory")
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
server:
  addr: ":9090"
log_level: "debug"
database:
  url: "postgres://localhost:5432/capabot-test?sslmode=disable"
providers:
  anthropic:
    api_key: "test-key-123"
    model: "claude-sonnet-4-6-20250514"
agent:
  max_iterations: 10
  context_budget_pct: 0.7
  max_tool_output_tokens: 2048
skills:
  dirs:
    - "/custom/skills"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Addr != ":9090" {
		t.Errorf("expected addr :9090, got %s", cfg.Server.Addr)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log level debug, got %s", cfg.LogLevel)
	}
	if cfg.Database.URL != "postgres://localhost:5432/capabot-test?sslmode=disable" {
		t.Errorf("expected db URL postgres://localhost:5432/capabot-test?sslmode=disable, got %s", cfg.Database.URL)
	}
	if cfg.Providers.Anthropic.APIKey != "test-key-123" {
		t.Errorf("expected api key test-key-123, got %s", cfg.Providers.Anthropic.APIKey)
	}
	if cfg.Providers.Anthropic.Model != "claude-sonnet-4-6-20250514" {
		t.Errorf("expected model claude-sonnet-4-6-20250514, got %s", cfg.Providers.Anthropic.Model)
	}
	if cfg.Agent.MaxIterations != 10 {
		t.Errorf("expected max iterations 10, got %d", cfg.Agent.MaxIterations)
	}
	if cfg.Agent.ContextBudgetPct != 0.7 {
		t.Errorf("expected context budget 0.7, got %f", cfg.Agent.ContextBudgetPct)
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
server:
  addr: ":9090"
log_level: "info"
providers:
  anthropic:
    api_key: "file-key"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CAPABOT_LOG_LEVEL", "warn")
	t.Setenv("CAPABOT_SERVER_ADDR", ":7070")
	t.Setenv("CAPABOT_ANTHROPIC_API_KEY", "env-key")
	t.Setenv("CAPABOT_DATABASE_URL", "postgres://localhost:5432/envdb?sslmode=disable")

	cfg, err := LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LogLevel != "warn" {
		t.Errorf("expected env override log level warn, got %s", cfg.LogLevel)
	}
	if cfg.Server.Addr != ":7070" {
		t.Errorf("expected env override addr :7070, got %s", cfg.Server.Addr)
	}
	if cfg.Providers.Anthropic.APIKey != "env-key" {
		t.Errorf("expected env override api key env-key, got %s", cfg.Providers.Anthropic.APIKey)
	}
	if cfg.Database.URL != "postgres://localhost:5432/envdb?sslmode=disable" {
		t.Errorf("expected env override db URL, got %s", cfg.Database.URL)
	}
}

func TestLoadConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid minimal config",
			yaml: `
server:
  addr: ":8080"
`,
			wantErr: false,
		},
		{
			name: "invalid addr missing colon",
			yaml: `
server:
  addr: "8080"
`,
			wantErr: true,
		},
		{
			name: "invalid log level",
			yaml: `
log_level: "verbose"
`,
			wantErr: true,
		},
		{
			name: "invalid agent max iterations zero",
			yaml: `
agent:
  max_iterations: 0
`,
			wantErr: true,
		},
		{
			name: "invalid context budget over 1",
			yaml: `
agent:
  context_budget_pct: 1.5
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := LoadFromFile(cfgPath)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestLoadConfig_MergeWithDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Partial config — only override server addr, everything else should be defaults
	content := `
server:
  addr: ":3000"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Addr != ":3000" {
		t.Errorf("expected addr :3000, got %s", cfg.Server.Addr)
	}
	// Defaults should still be present
	if cfg.LogLevel != "info" {
		t.Errorf("expected default log level info, got %s", cfg.LogLevel)
	}
	if cfg.Agent.MaxIterations != 25 {
		t.Errorf("expected default max iterations 25, got %d", cfg.Agent.MaxIterations)
	}
}
