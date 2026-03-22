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

// newTestOpenAIProvider creates an OpenAIProvider pointed at a test server.
func newTestOpenAIProvider(t *testing.T, handler http.HandlerFunc) *OpenAIProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &OpenAIProvider{
		apiKey:     "test-key",
		model:      "gpt-4o",
		baseURL:    server.URL,
		httpClient: &http.Client{},
	}
}

func TestOpenAIProvider_Name(t *testing.T) {
	p := &OpenAIProvider{model: "gpt-4o"}
	if p.Name() != "openai" {
		t.Errorf("expected name 'openai', got %q", p.Name())
	}
}

func TestOpenAIProvider_Models(t *testing.T) {
	p := &OpenAIProvider{model: "gpt-4o"}
	models := p.Models()
	if len(models) == 0 {
		t.Fatal("expected at least one model")
	}
	found := false
	for _, m := range models {
		if m.ID == "gpt-4o" {
			found = true
			if m.ContextWindow != 128000 {
				t.Errorf("expected 128K context window, got %d", m.ContextWindow)
			}
		}
	}
	if !found {
		t.Error("expected gpt-4o in models list")
	}
}

func TestOpenAIProvider_Chat_TextResponse(t *testing.T) {
	provider := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer auth header")
		}

		resp := openAIResponse{}
		resp.Choices = []struct {
			Message struct {
				Content   string           `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			{
				Message: struct {
					Content   string           `json:"content"`
					ToolCalls []openAIToolCall `json:"tool_calls"`
				}{Content: "Golang is great for systems programming."},
				FinishReason: "stop",
			},
		}
		resp.Usage.PromptTokens = 8
		resp.Usage.CompletionTokens = 12
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	result, err := provider.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Tell me about Go"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "Golang") {
		t.Errorf("unexpected content: %s", result.Content)
	}
	if result.StopReason != "stop" {
		t.Errorf("expected stop_reason 'stop', got %q", result.StopReason)
	}
	if result.Usage.InputTokens != 8 {
		t.Errorf("expected 8 input tokens, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 12 {
		t.Errorf("expected 12 output tokens, got %d", result.Usage.OutputTokens)
	}
}

func TestOpenAIProvider_Chat_ToolCallResponse(t *testing.T) {
	provider := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify tools are in request
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody["tools"] == nil {
			t.Error("expected tools in request")
		}

		tc := openAIToolCall{ID: "call-abc", Type: "function"}
		tc.Function.Name = "web_search"
		tc.Function.Arguments = `{"query":"latest Go release"}`

		resp := openAIResponse{}
		resp.Choices = []struct {
			Message struct {
				Content   string           `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			{
				Message: struct {
					Content   string           `json:"content"`
					ToolCalls []openAIToolCall `json:"tool_calls"`
				}{ToolCalls: []openAIToolCall{tc}},
				FinishReason: "tool_calls",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	result, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Latest Go?"}},
		Tools:    []ToolDefinition{{Name: "web_search", Description: "Search", InputSchema: schema}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls in response")
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call-abc" {
		t.Errorf("expected tool id 'call-abc', got %q", tc.ID)
	}
	if tc.Name != "web_search" {
		t.Errorf("expected tool name 'web_search', got %q", tc.Name)
	}
	var args map[string]any
	json.Unmarshal(tc.Input, &args)
	if args["query"] != "latest Go release" {
		t.Errorf("unexpected tool args: %v", args)
	}
	if result.StopReason != "tool_use" {
		t.Errorf("expected stop_reason 'tool_use', got %q", result.StopReason)
	}
}

func TestOpenAIProvider_Chat_HTTPError(t *testing.T) {
	provider := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	})

	_, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestOpenAIProvider_Stream_Text(t *testing.T) {
	provider := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		deltas := []string{"Hello", " there", " world"}
		for i, delta := range deltas {
			isLast := i == len(deltas)-1
			finishReason := ""
			if isLast {
				finishReason = "stop"
			}
			event := map[string]any{
				"choices": []map[string]any{
					{
						"delta":         map[string]any{"content": delta},
						"finish_reason": finishReason,
					},
				},
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
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
	if fullText != "Hello there world" {
		t.Errorf("expected 'Hello there world', got %q", fullText)
	}
}

func TestOpenAIProvider_Stream_HTTPError(t *testing.T) {
	provider := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
	})

	_, err := provider.Stream(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Hello"}},
		MaxTokens: 50,
	})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
}

func TestConvertOpenAIMessages_SystemPrompt(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	result := convertOpenAIMessages(msgs, "You are helpful.")
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected first message role 'system', got %q", result[0].Role)
	}
	if result[0].Content != "You are helpful." {
		t.Errorf("expected system content, got %q", result[0].Content)
	}
	if result[1].Role != "user" {
		t.Errorf("expected second message role 'user', got %q", result[1].Role)
	}
}

func TestConvertOpenAIMessages_ToolResult(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "tool",
			ToolResult: &ToolResult{
				ToolUseID: "call-1",
				Content:   "search result",
			},
		},
	}
	result := convertOpenAIMessages(msgs, "")
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "tool" {
		t.Errorf("expected role 'tool', got %q", result[0].Role)
	}
	if result[0].ToolCallID != "call-1" {
		t.Errorf("expected tool_call_id 'call-1', got %q", result[0].ToolCallID)
	}
	if result[0].Content != "search result" {
		t.Errorf("expected content 'search result', got %q", result[0].Content)
	}
}

func TestConvertOpenAIMessages_AssistantWithToolCalls(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call-1", Name: "search", Input: json.RawMessage(`{"q":"test"}`)},
			},
		},
	}
	result := convertOpenAIMessages(msgs, "")
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", result[0].Role)
	}
	if len(result[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result[0].ToolCalls))
	}
	if result[0].ToolCalls[0].ID != "call-1" {
		t.Errorf("expected tool call id 'call-1', got %q", result[0].ToolCalls[0].ID)
	}
	if result[0].ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected function name 'search', got %q", result[0].ToolCalls[0].Function.Name)
	}
}

func TestOpenAIProvider_Chat_NoSystemPrompt(t *testing.T) {
	var capturedBody map[string]any
	provider := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		resp := openAIResponse{}
		resp.Choices = []struct {
			Message struct {
				Content   string           `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			{
				Message: struct {
					Content   string           `json:"content"`
					ToolCalls []openAIToolCall `json:"tool_calls"`
				}{Content: "Hi"},
				FinishReason: "stop",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	provider.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Hi"}},
		MaxTokens: 10,
	})

	msgs, _ := capturedBody["messages"].([]any)
	for _, m := range msgs {
		msg := m.(map[string]any)
		if msg["role"] == "system" {
			t.Error("unexpected system message when no system prompt set")
		}
	}
}
