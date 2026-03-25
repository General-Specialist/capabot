package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/polymath/gostaff/internal/config"
	"github.com/polymath/gostaff/internal/llm"
)

type providerKeys struct {
	Anthropic  string `json:"anthropic"`
	OpenAI     string `json:"openai"`
	Gemini     string `json:"gemini"`
	OpenRouter string `json:"openrouter"`
}

func maskKey(k string) string {
	if len(k) <= 4 {
		return k
	}
	return k[:4] + "••••••••••••••••"
}

func (s *Server) handleConfigKeysGet(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		writeJSON(w, providerKeys{})
		return
	}
	cfg, err := config.LoadFromFile(s.configPath)
	if err != nil {
		writeJSON(w, providerKeys{})
		return
	}
	writeJSON(w, providerKeys{
		Anthropic:  maskKey(cfg.Providers.Anthropic.APIKey),
		OpenAI:     maskKey(cfg.Providers.OpenAI.APIKey),
		Gemini:     maskKey(cfg.Providers.Gemini.APIKey),
		OpenRouter: maskKey(cfg.Providers.OpenRouter.APIKey),
	})
}

func (s *Server) handleConfigKeysPut(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		http.Error(w, "config path not set", http.StatusInternalServerError)
		return
	}
	var body providerKeys
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	isPlaceholder := func(v string) bool {
		return v == "" || (len(v) > 4 && v[4:] == "••••••••••••••••")
	}

	pairs := []struct{ key, val string }{
		{"providers.anthropic.api_key", body.Anthropic},
		{"providers.openai.api_key", body.OpenAI},
		{"providers.gemini.api_key", body.Gemini},
		{"providers.openrouter.api_key", body.OpenRouter},
	}
	for _, p := range pairs {
		if isPlaceholder(p.val) {
			continue
		}
		if err := config.SetKey(s.configPath, p.key, p.val); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Reload providers in memory so changes take effect immediately.
	if s.router != nil {
		cfg, err := config.LoadFromFile(s.configPath)
		if err == nil {
			s.reloadProviders(r.Context(), cfg)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reloadProviders(ctx context.Context, cfg config.Config) {
	if cfg.Providers.Anthropic.APIKey != "" {
		s.router.SetProvider("anthropic", llm.NewAnthropicProvider(llm.AnthropicConfig{
			APIKey: cfg.Providers.Anthropic.APIKey,
			Model:  cfg.Providers.Anthropic.Model,
		}))
	}
	if cfg.Providers.OpenAI.APIKey != "" {
		s.router.SetProvider("openai", llm.NewOpenAIProvider(llm.OpenAIConfig{
			APIKey:  cfg.Providers.OpenAI.APIKey,
			BaseURL: cfg.Providers.OpenAI.BaseURL,
			Model:   cfg.Providers.OpenAI.Model,
		}))
	}
	if cfg.Providers.OpenRouter.APIKey != "" {
		s.router.SetProvider("openrouter", llm.NewOpenRouterProvider(llm.OpenRouterConfig{
			APIKey:  cfg.Providers.OpenRouter.APIKey,
			Model:   cfg.Providers.OpenRouter.Model,
			AppName: cfg.Providers.OpenRouter.AppName,
			SiteURL: cfg.Providers.OpenRouter.SiteURL,
		}))
	}
	// Gemini requires a context for its gRPC connection.
	if cfg.Providers.Gemini.APIKey != "" {
		if p, err := llm.NewGeminiProvider(ctx, llm.GeminiConfig{
			APIKey: cfg.Providers.Gemini.APIKey,
			Model:  cfg.Providers.Gemini.Model,
		}); err == nil {
			s.router.SetProvider("gemini", p)
		}
	}
}
