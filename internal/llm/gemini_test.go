package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// newTestGeminiProvider creates a GeminiProvider pointed at a test server.
func newTestGeminiProvider(t *testing.T, handler http.HandlerFunc) *GeminiProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: server.URL,
		},
	})
	if err != nil {
		t.Fatalf("creating test client: %v", err)
	}

	return &GeminiProvider{
		client: client,
		model:  "gemini-3-flash-preview",
	}
}

func TestGeminiProvider_Name(t *testing.T) {
	p := &GeminiProvider{model: "gemini-3-flash-preview"}
	if p.Name() != "gemini" {
		t.Errorf("expected name 'gemini', got %q", p.Name())
	}
}

func TestGeminiProvider_Models(t *testing.T) {
	p := &GeminiProvider{model: "gemini-3-flash-preview"}
	models := p.Models()
	if len(models) == 0 {
		t.Error("expected at least one model")
	}

	found := false
	for _, m := range models {
		if m.ID == "gemini-3-flash-preview" {
			found = true
			if m.ContextWindow != 1000000 {
				t.Errorf("expected 1M context window, got %d", m.ContextWindow)
			}
		}
	}
	if !found {
		t.Error("expected gemini-3-flash-preview in models list")
	}
}

func TestGeminiProvider_Chat(t *testing.T) {
	provider := newTestGeminiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		// Return a valid Gemini response
		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{"text": "AI works by processing data through neural networks."},
						},
						"role": "model",
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     10,
				"candidatesTokenCount": 15,
				"totalTokenCount":      25,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	result, err := provider.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "Explain how AI works"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content == "" {
		t.Error("expected non-empty content")
	}
	if !strings.Contains(result.Content, "neural networks") {
		t.Errorf("unexpected content: %s", result.Content)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 15 {
		t.Errorf("expected 15 output tokens, got %d", result.Usage.OutputTokens)
	}
}

func TestGeminiProvider_ToolUse(t *testing.T) {
	callCount := 0
	provider := newTestGeminiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		if callCount == 1 {
			// First call: model wants to call a tool
			resp := map[string]any{
				"candidates": []map[string]any{
					{
						"content": map[string]any{
							"parts": []map[string]any{
								{
									"functionCall": map[string]any{
										"name": "web_search",
										"args": map[string]any{
											"query": "current weather in SF",
										},
									},
								},
							},
							"role": "model",
						},
						"finishReason": "STOP",
					},
				},
				"usageMetadata": map[string]any{
					"promptTokenCount":     20,
					"candidatesTokenCount": 10,
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	})

	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	result, err := provider.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "What's the weather in SF?"}},
		MaxTokens: 200,
		Tools: []ToolDefinition{
			{Name: "web_search", Description: "Search the web", InputSchema: schema},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls in response")
	}
	if result.ToolCalls[0].Name != "web_search" {
		t.Errorf("expected tool call 'web_search', got %q", result.ToolCalls[0].Name)
	}

	var args map[string]any
	json.Unmarshal(result.ToolCalls[0].Input, &args)
	if args["query"] != "current weather in SF" {
		t.Errorf("unexpected tool args: %v", args)
	}
}

func TestGeminiProvider_Stream(t *testing.T) {
	provider := newTestGeminiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a streaming request (URL contains :streamGenerateContent)
		if !strings.Contains(r.URL.Path, "streamGenerateContent") {
			// Non-streaming fallback
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"candidates": []map[string]any{
					{
						"content": map[string]any{
							"parts": []map[string]any{{"text": "Hello world"}},
							"role":  "model",
						},
						"finishReason": "STOP",
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Gemini streaming uses text/event-stream with SSE
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		chunks := []map[string]any{
			{
				"candidates": []map[string]any{
					{
						"content": map[string]any{
							"parts": []map[string]any{{"text": "Hello"}},
							"role":  "model",
						},
					},
				},
			},
			{
				"candidates": []map[string]any{
					{
						"content": map[string]any{
							"parts": []map[string]any{{"text": " world"}},
							"role":  "model",
						},
						"finishReason": "STOP",
					},
				},
				"usageMetadata": map[string]any{
					"promptTokenCount":     5,
					"candidatesTokenCount": 3,
				},
			},
		}

		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
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
	if fullText == "" {
		t.Error("expected non-empty streamed text")
	}
}

func TestGeminiProvider_SystemInstruction(t *testing.T) {
	provider := newTestGeminiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		// Verify system instruction is present
		if _, ok := req["systemInstruction"]; !ok {
			t.Error("expected systemInstruction in request")
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{{"text": "I am a helpful bot."}},
						"role":  "model",
					},
					"finishReason": "STOP",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	result, err := provider.Chat(context.Background(), ChatRequest{
		System:    "You are a helpful assistant named Capabot.",
		Messages:  []ChatMessage{{Role: "user", Content: "Who are you?"}},
		MaxTokens: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestConvertMessages(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you?"},
	}

	contents := convertMessages(messages)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}

	if contents[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", contents[0].Role)
	}
	if contents[1].Role != "model" {
		t.Errorf("expected role 'model', got %q", contents[1].Role)
	}
}

func TestConvertMessages_ToolResult(t *testing.T) {
	messages := []ChatMessage{
		{
			Role: "tool",
			ToolResult: &ToolResult{
				ToolUseID: "search-123",
				Content:   "Result: sunny and 72°F",
				IsError:   false,
			},
		},
	}

	contents := convertMessages(messages)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}

	// Should have a function response part
	found := false
	for _, part := range contents[0].Parts {
		if part.FunctionResponse != nil {
			found = true
			if part.FunctionResponse.Name != "search-123" {
				t.Errorf("expected function response name 'search-123', got %q", part.FunctionResponse.Name)
			}
		}
	}
	if !found {
		t.Error("expected function response part")
	}
}

func TestConvertTools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`)
	tools := []ToolDefinition{
		{Name: "web_fetch", Description: "Fetch a URL", InputSchema: schema},
		{Name: "file_read", Description: "Read a file", InputSchema: nil},
	}

	result := convertTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool group, got %d", len(result))
	}
	if len(result[0].FunctionDeclarations) != 2 {
		t.Fatalf("expected 2 function declarations, got %d", len(result[0].FunctionDeclarations))
	}
	if result[0].FunctionDeclarations[0].Name != "web_fetch" {
		t.Errorf("expected name 'web_fetch', got %q", result[0].FunctionDeclarations[0].Name)
	}
}

func TestExtractResponse_Nil(t *testing.T) {
	_, err := extractResponse(nil)
	if err == nil {
		t.Error("expected error for nil response")
	}
}

func TestExtractResponse_Empty(t *testing.T) {
	resp := &genai.GenerateContentResponse{}
	result, err := extractResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
}
