package llm_test

import (
	"testing"

	"github.com/polymath/capabot/internal/llm"
)

func TestOpenRouterProvider_Name(t *testing.T) {
	p := llm.NewOpenRouterProvider(llm.OpenRouterConfig{
		APIKey: "test-key",
	})
	if p.Name() != "openrouter" {
		t.Errorf("want name=openrouter, got %q", p.Name())
	}
}

func TestOpenRouterProvider_DefaultModel(t *testing.T) {
	p := llm.NewOpenRouterProvider(llm.OpenRouterConfig{
		APIKey: "test-key",
	})
	models := p.Models()
	if len(models) == 0 {
		t.Fatal("expected at least one model")
	}
	// Default model should be in the list
	found := false
	for _, m := range models {
		if m.ID == "anthropic/claude-sonnet-4-6" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected default model anthropic/claude-sonnet-4-6 in Models()")
	}
}

func TestOpenRouterProvider_Models(t *testing.T) {
	p := llm.NewOpenRouterProvider(llm.OpenRouterConfig{APIKey: "k"})
	models := p.Models()
	if len(models) < 5 {
		t.Errorf("expected at least 5 models, got %d", len(models))
	}
	for _, m := range models {
		if m.ID == "" {
			t.Error("model with empty ID")
		}
	}
}
