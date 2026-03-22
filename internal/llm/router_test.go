package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// mockProvider is a simple in-memory Provider for testing.
type mockProvider struct {
	name       string
	models     []ModelInfo
	chatResp   *ChatResponse
	chatErr    error
	streamResp []StreamChunk
	streamErr  error
	chatCalls  int
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Models() []ModelInfo { return m.models }

func (m *mockProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	m.chatCalls++
	return m.chatResp, m.chatErr
}

func (m *mockProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	ch := make(chan StreamChunk, len(m.streamResp)+1)
	for _, chunk := range m.streamResp {
		ch <- chunk
	}
	ch <- StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

// retryableError wraps an HTTPStatusError to simulate provider failure.
func retryableError(code int) error {
	return fmt.Errorf("provider failed: %w", &HTTPStatusError{StatusCode: code, Body: "error"})
}

func TestRouter_Name(t *testing.T) {
	r := NewRouter(RouterConfig{Primary: "a"}, map[string]Provider{})
	if r.Name() != "router" {
		t.Errorf("expected name 'router', got %q", r.Name())
	}
}

func TestRouter_Models_Union(t *testing.T) {
	provA := &mockProvider{
		name:   "a",
		models: []ModelInfo{{ID: "model-a", ContextWindow: 10000}},
	}
	provB := &mockProvider{
		name:   "b",
		models: []ModelInfo{{ID: "model-b", ContextWindow: 20000}},
	}
	r := NewRouter(RouterConfig{Primary: "a", Fallbacks: []string{"b"}}, map[string]Provider{
		"a": provA,
		"b": provB,
	})

	models := r.Models()
	if len(models) != 2 {
		t.Fatalf("expected 2 models (union), got %d", len(models))
	}

	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}
	if !ids["model-a"] {
		t.Error("expected model-a in union")
	}
	if !ids["model-b"] {
		t.Error("expected model-b in union")
	}
}

func TestRouter_Chat_PrimarySucceeds(t *testing.T) {
	primary := &mockProvider{
		name:     "primary",
		chatResp: &ChatResponse{Content: "from primary"},
	}
	fallback := &mockProvider{
		name:     "fallback",
		chatResp: &ChatResponse{Content: "from fallback"},
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"fallback"}}, map[string]Provider{
		"primary":  primary,
		"fallback": fallback,
	})

	resp, err := r.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from primary" {
		t.Errorf("expected response from primary, got %q", resp.Content)
	}
	if primary.chatCalls != 1 {
		t.Errorf("expected 1 primary call, got %d", primary.chatCalls)
	}
	if fallback.chatCalls != 0 {
		t.Errorf("expected 0 fallback calls, got %d", fallback.chatCalls)
	}
}

func TestRouter_Chat_PrimaryRetryable_FallbackUsed(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: retryableError(429),
	}
	fallback := &mockProvider{
		name:     "fallback",
		chatResp: &ChatResponse{Content: "from fallback"},
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"fallback"}}, map[string]Provider{
		"primary":  primary,
		"fallback": fallback,
	})

	resp, err := r.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from fallback" {
		t.Errorf("expected response from fallback, got %q", resp.Content)
	}
	if primary.chatCalls != 1 {
		t.Errorf("expected 1 primary call, got %d", primary.chatCalls)
	}
	if fallback.chatCalls != 1 {
		t.Errorf("expected 1 fallback call, got %d", fallback.chatCalls)
	}
}

func TestRouter_Chat_PrimaryNonRetryable_NoFallback(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: retryableError(401), // 401 is not retryable
	}
	fallback := &mockProvider{
		name:     "fallback",
		chatResp: &ChatResponse{Content: "from fallback"},
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"fallback"}}, map[string]Provider{
		"primary":  primary,
		"fallback": fallback,
	})

	_, err := r.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error for non-retryable failure")
	}
	if fallback.chatCalls != 0 {
		t.Errorf("expected 0 fallback calls for non-retryable error, got %d", fallback.chatCalls)
	}
}

func TestRouter_Chat_AllFail(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: retryableError(503),
	}
	fallback := &mockProvider{
		name:    "fallback",
		chatErr: retryableError(503),
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"fallback"}}, map[string]Provider{
		"primary":  primary,
		"fallback": fallback,
	})

	_, err := r.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestRouter_Chat_5xxRetryable(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: retryableError(500),
	}
	fallback := &mockProvider{
		name:     "fallback",
		chatResp: &ChatResponse{Content: "fallback ok"},
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"fallback"}}, map[string]Provider{
		"primary":  primary,
		"fallback": fallback,
	})

	resp, err := r.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "fallback ok" {
		t.Errorf("expected 'fallback ok', got %q", resp.Content)
	}
}

func TestRouter_Stream_PrimarySucceeds(t *testing.T) {
	primary := &mockProvider{
		name:       "primary",
		streamResp: []StreamChunk{{Delta: "hello"}},
	}
	r := NewRouter(RouterConfig{Primary: "primary"}, map[string]Provider{
		"primary": primary,
	})

	ch, err := r.Stream(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var text string
	for chunk := range ch {
		text += chunk.Delta
	}
	if text != "hello" {
		t.Errorf("expected 'hello', got %q", text)
	}
}

func TestRouter_Stream_PrimaryRetryable_FallbackUsed(t *testing.T) {
	primary := &mockProvider{
		name:      "primary",
		streamErr: retryableError(429),
	}
	fallback := &mockProvider{
		name:       "fallback",
		streamResp: []StreamChunk{{Delta: "fallback response"}},
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"fallback"}}, map[string]Provider{
		"primary":  primary,
		"fallback": fallback,
	})

	ch, err := r.Stream(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var text string
	for chunk := range ch {
		text += chunk.Delta
	}
	if text != "fallback response" {
		t.Errorf("expected 'fallback response', got %q", text)
	}
}

func TestRouter_Stream_AllFail(t *testing.T) {
	primary := &mockProvider{
		name:      "primary",
		streamErr: retryableError(503),
	}
	fallback := &mockProvider{
		name:      "fallback",
		streamErr: retryableError(503),
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"fallback"}}, map[string]Provider{
		"primary":  primary,
		"fallback": fallback,
	})

	_, err := r.Stream(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, false},
		{"429", &HTTPStatusError{StatusCode: 429}, true},
		{"500", &HTTPStatusError{StatusCode: 500}, true},
		{"503", &HTTPStatusError{StatusCode: 503}, true},
		{"401", &HTTPStatusError{StatusCode: 401}, false},
		{"400", &HTTPStatusError{StatusCode: 400}, false},
		{"wrapped 429", fmt.Errorf("wrap: %w", &HTTPStatusError{StatusCode: 429}), true},
		{"wrapped 401", fmt.Errorf("wrap: %w", &HTTPStatusError{StatusCode: 401}), false},
		{"plain error", errors.New("plain"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryable(tt.err)
			if got != tt.expected {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestRouter_MissingProvider_Skipped(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: retryableError(429),
	}
	// "fallback" is listed but not in map — should be skipped
	actual := &mockProvider{
		name:     "actual",
		chatResp: &ChatResponse{Content: "actual fallback"},
	}
	r := NewRouter(RouterConfig{Primary: "primary", Fallbacks: []string{"missing", "actual"}}, map[string]Provider{
		"primary": primary,
		"actual":  actual,
	})

	resp, err := r.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "actual fallback" {
		t.Errorf("expected 'actual fallback', got %q", resp.Content)
	}
}
