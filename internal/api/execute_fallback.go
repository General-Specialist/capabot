package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/polymath/gostaff/internal/llm"
)

func (s *Server) handleExecuteFallbackGet(w http.ResponseWriter, r *http.Request) {
	v, _ := s.store.GetSetting(r.Context(), "execute_fallback")
	writeJSON(w, map[string]bool{"enabled": v == "true"})
}

func (s *Server) handleExecuteFallbackPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	val := "false"
	if body.Enabled {
		val = "true"
	}
	if err := s.store.SetSetting(r.Context(), "execute_fallback", val); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if body.Enabled {
		s.reloadExecuteFallback(r.Context())
	} else {
		s.router.SetFallbacks(nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

// RestoreExecuteFallback is called at server startup to re-apply the execute
// fallback if it was previously enabled.
func (s *Server) RestoreExecuteFallback(ctx context.Context) {
	v, _ := s.store.GetSetting(ctx, "execute_fallback")
	if v == "true" {
		s.reloadExecuteFallback(ctx)
	}
}

// reloadExecuteFallback loads the execute mode's API keys and registers them
// as fallback providers in the router. Called when the setting is toggled on
// or when the execute mode's keys change while the setting is active.
func (s *Server) reloadExecuteFallback(ctx context.Context) {
	if s.router == nil || s.store == nil {
		return
	}

	keys, err := s.store.GetMode(ctx, "execute")
	if err != nil {
		return
	}

	var fallbacks []string

	if keys.Anthropic != "" {
		s.router.SetProvider("execute-anthropic", llm.NewAnthropicProvider(llm.AnthropicConfig{
			APIKey: keys.Anthropic,
		}))
		fallbacks = append(fallbacks, "execute-anthropic")
	}
	if keys.OpenAI != "" {
		s.router.SetProvider("execute-openai", llm.NewOpenAIProvider(llm.OpenAIConfig{
			APIKey: keys.OpenAI,
		}))
		fallbacks = append(fallbacks, "execute-openai")
	}
	if keys.OpenRouter != "" {
		s.router.SetProvider("execute-openrouter", llm.NewOpenRouterProvider(llm.OpenRouterConfig{
			APIKey: keys.OpenRouter,
		}))
		fallbacks = append(fallbacks, "execute-openrouter")
	}
	if keys.Gemini != "" {
		if p, err := llm.NewGeminiProvider(ctx, llm.GeminiConfig{APIKey: keys.Gemini}); err == nil {
			s.router.SetProvider("execute-gemini", p)
			fallbacks = append(fallbacks, "execute-gemini")
		}
	}

	s.router.SetFallbacks(fallbacks)
}
