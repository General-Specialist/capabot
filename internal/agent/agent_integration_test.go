package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/polymath/gostaff/internal/llm"
	"github.com/rs/zerolog"
)

// Integration tests — skipped unless GEMINI_API_KEY is set.
// Run with: GEMINI_API_KEY=... go test ./internal/agent/ -run Integration -v

func skipWithoutKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set, skipping integration test")
	}
	return key
}

func TestIntegration_AgentSimpleChat(t *testing.T) {
	key := skipWithoutKey(t)
	ctx := context.Background()

	provider, err := llm.NewGeminiProvider(ctx, llm.GeminiConfig{
		APIKey: key,
		Model:  "gemini-3-flash-preview",
	})
	if err != nil {
		t.Fatalf("creating provider: %v", err)
	}

	logger := zerolog.New(zerolog.NewTestWriter(t)).With().Timestamp().Logger()
	ctxMgr := NewContextManager(ContextConfig{
		ContextWindow:       1000000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 4096,
	})

	agent := New(AgentConfig{
		ID:            "test-agent",
		MaxIterations: 5,
		MaxTokens:     256,
		SystemPrompt:  "You are a helpful assistant. Be concise.",
	}, provider, NewRegistry(), ctxMgr, logger)

	result, err := agent.Run(ctx, "", []llm.ChatMessage{
		{Role: "user", Content: "Reply with exactly: PONG"},
	})
	if err != nil {
		t.Fatalf("agent run error: %v", err)
	}

	t.Logf("response: %q", result.Response)
	t.Logf("iterations: %d, tool_calls: %d", result.Iterations, result.ToolCalls)
	t.Logf("usage: in=%d out=%d", result.Usage.InputTokens, result.Usage.OutputTokens)

	if result.Response == "" {
		t.Error("expected non-empty response")
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration for simple chat, got %d", result.Iterations)
	}
}

func TestIntegration_AgentToolUse(t *testing.T) {
	key := skipWithoutKey(t)
	ctx := context.Background()

	provider, err := llm.NewGeminiProvider(ctx, llm.GeminiConfig{
		APIKey: key,
		Model:  "gemini-3-flash-preview",
	})
	if err != nil {
		t.Fatalf("creating provider: %v", err)
	}

	logger := zerolog.New(zerolog.NewTestWriter(t)).With().Timestamp().Logger()
	ctxMgr := NewContextManager(ContextConfig{
		ContextWindow:       1000000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 4096,
	})

	tools := NewRegistry()
	_ = tools.Register(&stubTool{
		name: "get_weather",
		desc: "Get the current weather for a city. Returns a JSON object with temperature and conditions.",
		params: json.RawMessage(`{
			"type": "object",
			"properties": {
				"city": {"type": "string", "description": "The city name"}
			},
			"required": ["city"]
		}`),
		fn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			var input struct {
				City string `json:"city"`
			}
			json.Unmarshal(params, &input)
			return ToolResult{
				Content: `{"city":"` + input.City + `","temperature":"72°F","conditions":"sunny"}`,
			}, nil
		},
	})

	agent := New(AgentConfig{
		ID:            "test-agent",
		MaxIterations: 5,
		MaxTokens:     512,
		SystemPrompt:  "You are a helpful assistant. When asked about weather, use the get_weather tool. Be concise.",
	}, provider, tools, ctxMgr, logger)

	result, err := agent.Run(ctx, "", []llm.ChatMessage{
		{Role: "user", Content: "What's the weather in Tokyo?"},
	})
	if err != nil {
		t.Fatalf("agent run error: %v", err)
	}

	t.Logf("response: %q", result.Response)
	t.Logf("iterations: %d, tool_calls: %d", result.Iterations, result.ToolCalls)
	t.Logf("usage: in=%d out=%d", result.Usage.InputTokens, result.Usage.OutputTokens)

	if result.Response == "" {
		t.Error("expected non-empty response")
	}
	if result.ToolCalls < 1 {
		t.Error("expected at least 1 tool call")
	}
	if !strings.Contains(strings.ToLower(result.Response), "tokyo") &&
		!strings.Contains(strings.ToLower(result.Response), "72") &&
		!strings.Contains(strings.ToLower(result.Response), "sunny") {
		t.Errorf("response doesn't seem to reference weather data: %q", result.Response)
	}
}
