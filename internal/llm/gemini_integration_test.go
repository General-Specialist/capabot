package llm

import (
	"context"
	"os"
	"testing"
)

// Integration tests — skipped unless GEMINI_API_KEY is set.
// Run with: GEMINI_API_KEY=... go test ./internal/llm/ -run Integration -v

func skipWithoutKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set, skipping integration test")
	}
	return key
}

func TestIntegration_GeminiChat(t *testing.T) {
	key := skipWithoutKey(t)

	ctx := context.Background()
	provider, err := NewGeminiProvider(ctx, GeminiConfig{
		APIKey: key,
		Model:  "gemini-3-flash-preview",
	})
	if err != nil {
		t.Fatalf("creating provider: %v", err)
	}

	resp, err := provider.Chat(ctx, ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Reply with exactly: PONG"}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}

	t.Logf("response: %q", resp.Content)
	t.Logf("usage: in=%d out=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)

	if resp.Content == "" {
		t.Error("expected non-empty response")
	}
}

func TestIntegration_GeminiStream(t *testing.T) {
	key := skipWithoutKey(t)

	ctx := context.Background()
	provider, err := NewGeminiProvider(ctx, GeminiConfig{
		APIKey: key,
		Model:  "gemini-3-flash-preview",
	})
	if err != nil {
		t.Fatalf("creating provider: %v", err)
	}

	ch, err := provider.Stream(ctx, ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Count from 1 to 5, one number per line."}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	var fullText string
	var gotDone bool
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("chunk error: %v", chunk.Err)
		}
		fullText += chunk.Delta
		if chunk.Done {
			gotDone = true
		}
	}

	t.Logf("streamed: %q", fullText)

	if fullText == "" {
		t.Error("expected non-empty streamed text")
	}
	if !gotDone {
		t.Error("expected done signal")
	}
}
