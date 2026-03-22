package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestAnthropicProvider creates an AnthropicProvider pointed at a test server.
func newTestAnthropicProvider(t *testing.T, handler http.HandlerFunc) *AnthropicProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AnthropicProvider{
		apiKey:     "test-key",
		model:      "claude-sonnet-4-6",
		baseURL:    server.URL,
		httpClient: &http.Client{},
	}
}

func TestAnthropicProvider_Name(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6"}
	if p.Name() != "anthropic" {
		t.Errorf("expected name 'anthropic', got %q", p.Name())
	}
}

func TestAnthropicProvider_Models(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6"}
	models := p.Models()
	if len(models) == 0 {
		t.Fatal("expected at least one model")
	}
	found := false
	for _, m := range models {
		if m.ID == "claude-sonnet-4-6" {
			found = true
			if m.ContextWindow != 200000 {
				t.Errorf("expected 200K context window, got %d", m.ContextWindow)
			}
		}
	}
	if !found {
		t.Error("expected claude-sonnet-4-6 in models list")
	}
}

func TestAnthropicProvider_Chat_TextResponse(t *testing.T) {
	provider := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify required headers
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}

		resp := anthropicResponse{
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Neural networks process data in layers."},
			},
			StopReason: "end_turn",
		}
		resp.Usage.InputTokens = 10
		resp.Usage.OutputTokens = 20
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	result, err := provider.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Explain AI"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "Neural networks") {
		t.Errorf("unexpected content: %s", result.Content)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", result.StopReason)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 20 {
		t.Errorf("expected 20 output tokens, got %d", result.Usage.OutputTokens)
	}
}

func TestAnthropicProvider_Chat_ToolCallResponse(t *testing.T) {
	provider := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []anthropicContentBlock{
				{
					Type:  "tool_use",
					ID:    "tool-123",
					Name:  "web_search",
					Input: json.RawMessage(`{"query":"current weather"}`),
				},
			},
			StopReason: "tool_use",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	result, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Weather?"}},
		Tools:    []ToolDefinition{{Name: "web_search", Description: "Search", InputSchema: schema}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls in response")
	}
	tc := result.ToolCalls[0]
	if tc.ID != "tool-123" {
		t.Errorf("expected tool id 'tool-123', got %q", tc.ID)
	}
	if tc.Name != "web_search" {
		t.Errorf("expected tool name 'web_search', got %q", tc.Name)
	}
	var args map[string]any
	json.Unmarshal(tc.Input, &args)
	if args["query"] != "current weather" {
		t.Errorf("unexpected tool args: %v", args)
	}
}

func TestAnthropicProvider_Chat_HTTPError(t *testing.T) {
	provider := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"type":"authentication_error"}}`, http.StatusUnauthorized)
	})

	_, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestAnthropicProvider_Stream_Text(t *testing.T) {
	provider := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","content":[]}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	ch, err := provider.Stream(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Say hello"}},
		MaxTokens: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fullText string
	var gotDone bool
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		fullText += chunk.Delta
		if chunk.Done {
			gotDone = true
		}
	}

	if !gotDone {
		t.Error("expected done signal in stream")
	}
	if fullText != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", fullText)
	}
}

func TestAnthropicProvider_Stream_HTTPError(t *testing.T) {
	provider := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"type":"overloaded"}}`, http.StatusServiceUnavailable)
	})

	_, err := provider.Stream(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Hello"}},
		MaxTokens: 50,
	})
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
}

func TestConvertAnthropicMessages_UserMessage(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	result := convertAnthropicMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", result[0].Role)
	}
	if result[0].Content.(string) != "hello" {
		t.Errorf("expected content 'hello', got %v", result[0].Content)
	}
}

func TestConvertAnthropicMessages_AssistantWithToolCalls(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role:    "assistant",
			Content: "Let me search",
			ToolCalls: []ToolCall{
				{ID: "tc-1", Name: "search", Input: json.RawMessage(`{"q":"test"}`)},
			},
		},
	}
	result := convertAnthropicMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	blocks, ok := result[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("expected []anthropicContentBlock, got %T", result[0].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (text + tool_use), got %d", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("expected first block type 'text', got %q", blocks[0].Type)
	}
	if blocks[1].Type != "tool_use" {
		t.Errorf("expected second block type 'tool_use', got %q", blocks[1].Type)
	}
	if blocks[1].ID != "tc-1" {
		t.Errorf("expected tool_use id 'tc-1', got %q", blocks[1].ID)
	}
}

func TestConvertAnthropicMessages_ToolResult(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "tool",
			ToolResult: &ToolResult{
				ToolUseID: "tc-1",
				Content:   "search result",
			},
		},
	}
	result := convertAnthropicMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("expected role 'user' for tool result, got %q", result[0].Role)
	}
	blocks, ok := result[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("expected []anthropicContentBlock, got %T", result[0].Content)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != "tool_result" {
		t.Errorf("expected block type 'tool_result', got %q", blocks[0].Type)
	}
	if blocks[0].ToolUseID != "tc-1" {
		t.Errorf("expected tool_use_id 'tc-1', got %q", blocks[0].ToolUseID)
	}
	if blocks[0].Content != "search result" {
		t.Errorf("expected content 'search result', got %q", blocks[0].Content)
	}
}

func TestAnthropicProvider_Chat_SystemPrompt(t *testing.T) {
	var capturedBody map[string]any
	provider := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		resp := anthropicResponse{
			Content:    []anthropicContentBlock{{Type: "text", Text: "I am Capabot."}},
			StopReason: "end_turn",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	provider.Chat(context.Background(), ChatRequest{
		System:    "You are Capabot.",
		Messages:  []ChatMessage{{Role: "user", Content: "Who are you?"}},
		MaxTokens: 50,
	})

	if capturedBody["system"] != "You are Capabot." {
		t.Errorf("expected system prompt in request body, got %v", capturedBody["system"])
	}
}
