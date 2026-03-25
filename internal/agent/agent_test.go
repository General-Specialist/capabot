package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/polymath/capabot/internal/llm"
	"github.com/rs/zerolog"
)

// mockProvider implements llm.Provider for testing the agent loop.
type mockProvider struct {
	responses []*llm.ChatResponse // responses to return in order
	callIdx   int
	calls     []llm.ChatRequest // recorded calls
}

func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.calls = append(m.calls, req)
	if m.callIdx >= len(m.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", m.callIdx+1)
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockProvider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("stream not implemented in mock")
}

func (m *mockProvider) Models() []llm.ModelInfo {
	return []llm.ModelInfo{{ID: "mock-model", Name: "Mock", ContextWindow: 128000}}
}

func (m *mockProvider) Name() string { return "mock" }

// mockStore implements StoreWriter for testing persistence.
type mockStore struct {
	messages       []StoreMessage
	toolExecutions []StoreToolExecution
}

func (ms *mockStore) SaveMessage(ctx context.Context, msg StoreMessage) (int64, error) {
	ms.messages = append(ms.messages, msg)
	return int64(len(ms.messages)), nil
}

func (ms *mockStore) SaveToolExecution(ctx context.Context, exec StoreToolExecution) error {
	ms.toolExecutions = append(ms.toolExecutions, exec)
	return nil
}

func (ms *mockStore) SaveUsage(ctx context.Context, usage StoreUsage) error {
	return nil
}

func newTestAgent(provider llm.Provider, tools *Registry) *Agent {
	logger := zerolog.Nop()
	ctxMgr := NewContextManager(ContextConfig{
		ContextWindow:       128000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 4096,
	})
	return New(AgentConfig{
		ID:            "test-agent",
		MaxIterations: 10,
		MaxTokens:     1024,
		SystemPrompt:  "You are a helpful assistant.",
	}, provider, tools, ctxMgr, logger)
}

func TestAgent_SimpleResponse(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{Content: "Hello!", StopReason: "STOP", Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}

	agent := newTestAgent(provider, NewRegistry())

	result, err := agent.Run(context.Background(), "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Response != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", result.Response)
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.ToolCalls != 0 {
		t.Errorf("expected 0 tool calls, got %d", result.ToolCalls)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", result.Usage.InputTokens)
	}
}

func TestAgent_ToolCallThenResponse(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			// First response: tool call
			{
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "tc-1", Name: "echo", Input: json.RawMessage(`{"msg":"hello"}`)},
				},
				StopReason: "TOOL_USE",
				Usage:      llm.Usage{InputTokens: 20, OutputTokens: 10},
			},
			// Second response: final answer
			{
				Content:    "The echo tool said: hello",
				StopReason: "STOP",
				Usage:      llm.Usage{InputTokens: 30, OutputTokens: 15},
			},
		},
	}

	tools := NewRegistry()
	_ = tools.Register(&stubTool{
		name:   "echo",
		desc:   "echoes input",
		params: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
		fn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			var input struct{ Msg string `json:"msg"` }
			json.Unmarshal(params, &input)
			return ToolResult{Content: input.Msg}, nil
		},
	})

	agent := newTestAgent(provider, tools)

	result, err := agent.Run(context.Background(), "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Echo hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Response != "The echo tool said: hello" {
		t.Errorf("unexpected response: %q", result.Response)
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
	if result.ToolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", result.ToolCalls)
	}

	// Verify the second LLM call includes the tool result
	if len(provider.calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(provider.calls))
	}
	secondCall := provider.calls[1]
	lastMsg := secondCall.Messages[len(secondCall.Messages)-1]
	if lastMsg.Role != "tool" {
		t.Errorf("expected last message role 'tool', got %q", lastMsg.Role)
	}
	if lastMsg.ToolResult == nil {
		t.Fatal("expected tool result in last message")
	}
	if lastMsg.ToolResult.Content != "hello" {
		t.Errorf("expected tool result 'hello', got %q", lastMsg.ToolResult.Content)
	}
}

func TestAgent_ToolNotFound(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc-1", Name: "nonexistent", Input: json.RawMessage(`{}`)},
				},
				Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{Content: "I see there was an error.", StopReason: "STOP", Usage: llm.Usage{InputTokens: 20, OutputTokens: 10}},
		},
	}

	agent := newTestAgent(provider, NewRegistry())

	result, err := agent.Run(context.Background(), "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Use a tool"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The agent should send an error tool result and continue
	if result.Response != "I see there was an error." {
		t.Errorf("unexpected response: %q", result.Response)
	}

	// Verify the error was sent as a tool result
	secondCall := provider.calls[1]
	lastMsg := secondCall.Messages[len(secondCall.Messages)-1]
	if !lastMsg.ToolResult.IsError {
		t.Error("expected tool result to be marked as error")
	}
}

func TestAgent_MaxIterations(t *testing.T) {
	// Create a provider that always returns tool calls (infinite loop)
	provider := &mockProvider{}
	for i := 0; i < 5; i++ {
		provider.responses = append(provider.responses, &llm.ChatResponse{
			ToolCalls: []llm.ToolCall{
				{ID: fmt.Sprintf("tc-%d", i), Name: "echo", Input: json.RawMessage(`{"msg":"loop"}`)},
			},
			Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
		})
	}

	tools := NewRegistry()
	_ = tools.Register(newStub("echo"))

	agent := newTestAgent(provider, tools)
	agent.config.MaxIterations = 3

	result, err := agent.Run(context.Background(), "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Loop forever"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StopReason != "max_iterations" {
		t.Errorf("expected stop reason 'max_iterations', got %q", result.StopReason)
	}
	if result.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", result.Iterations)
	}
}

func TestAgent_ContextCancellation(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{Content: "should not reach", Usage: llm.Usage{}},
		},
	}

	agent := newTestAgent(provider, NewRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := agent.Run(ctx, "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAgent_MultipleToolCalls(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc-1", Name: "alpha", Input: json.RawMessage(`{}`)},
					{ID: "tc-2", Name: "beta", Input: json.RawMessage(`{}`)},
				},
				Usage: llm.Usage{InputTokens: 20, OutputTokens: 10},
			},
			{Content: "Both tools executed.", StopReason: "STOP", Usage: llm.Usage{InputTokens: 40, OutputTokens: 15}},
		},
	}

	tools := NewRegistry()
	_ = tools.Register(newStub("alpha"))
	_ = tools.Register(newStub("beta"))

	agent := newTestAgent(provider, tools)

	result, err := agent.Run(context.Background(), "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Use both tools"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ToolCalls != 2 {
		t.Errorf("expected 2 tool calls, got %d", result.ToolCalls)
	}
	if result.Response != "Both tools executed." {
		t.Errorf("unexpected response: %q", result.Response)
	}
}

func TestAgent_Persistence(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc-1", Name: "echo", Input: json.RawMessage(`{"msg":"hi"}`)},
				},
				Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{Content: "Done!", StopReason: "STOP", Usage: llm.Usage{InputTokens: 20, OutputTokens: 10}},
		},
	}

	tools := NewRegistry()
	_ = tools.Register(newStub("echo"))

	store := &mockStore{}
	agent := newTestAgent(provider, tools)
	agent.SetStore(store)

	_, err := agent.Run(context.Background(), "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 persisted messages: 2 assistant + 1 tool result
	if len(store.messages) != 3 {
		t.Errorf("expected 3 persisted messages, got %d", len(store.messages))
	}

	// Should have 1 tool execution persisted
	if len(store.toolExecutions) != 1 {
		t.Errorf("expected 1 tool execution, got %d", len(store.toolExecutions))
	}
	if store.toolExecutions[0].ToolName != "echo" {
		t.Errorf("expected tool name 'echo', got %q", store.toolExecutions[0].ToolName)
	}
}

func TestAgent_ToolOutputTruncation(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "tc-1", Name: "big", Input: json.RawMessage(`{}`)},
				},
				Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{Content: "Got it.", StopReason: "STOP", Usage: llm.Usage{InputTokens: 20, OutputTokens: 10}},
		},
	}

	tools := NewRegistry()
	bigOutput := make([]byte, 100000) // ~25K tokens, way over 4096 limit
	for i := range bigOutput {
		bigOutput[i] = 'x'
	}
	_ = tools.Register(&stubTool{
		name:   "big",
		desc:   "returns huge output",
		params: json.RawMessage(`{"type":"object"}`),
		fn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			return ToolResult{Content: string(bigOutput)}, nil
		},
	})

	agent := newTestAgent(provider, tools)

	result, err := agent.Run(context.Background(), "sess-1", []llm.ChatMessage{
		{Role: "user", Content: "Get big data"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response != "Got it." {
		t.Errorf("unexpected response: %q", result.Response)
	}

	// Verify the tool result sent to LLM was truncated
	secondCall := provider.calls[1]
	lastMsg := secondCall.Messages[len(secondCall.Messages)-1]
	if len(lastMsg.ToolResult.Content) >= 100000 {
		t.Error("expected tool output to be truncated")
	}
}

func TestAgent_SystemPromptPassed(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{Content: "ok", StopReason: "STOP", Usage: llm.Usage{}},
		},
	}

	agent := newTestAgent(provider, NewRegistry())

	_, err := agent.Run(context.Background(), "", []llm.ChatMessage{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.calls[0].System != "You are a helpful assistant." {
		t.Errorf("expected system prompt, got %q", provider.calls[0].System)
	}
}

func TestAgent_ToolDefsPassedToLLM(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{Content: "ok", StopReason: "STOP", Usage: llm.Usage{}},
		},
	}

	tools := NewRegistry()
	_ = tools.Register(newStub("alpha"))
	_ = tools.Register(newStub("beta"))

	agent := newTestAgent(provider, tools)

	_, err := agent.Run(context.Background(), "", []llm.ChatMessage{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(provider.calls[0].Tools) != 2 {
		t.Errorf("expected 2 tool definitions, got %d", len(provider.calls[0].Tools))
	}
}
