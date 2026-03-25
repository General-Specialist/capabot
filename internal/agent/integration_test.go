package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/rs/zerolog"
)

// mockProvider is a test LLM provider that replays a scripted sequence of responses.
type mockProvider struct {
	responses []*llm.ChatResponse
	calls     int
}

func (m *mockProvider) Name() string        { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo { return nil }

func (m *mockProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.calls >= len(m.responses) {
		return &llm.ChatResponse{
			Content:    "done",
			StopReason: "end_turn",
		}, nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *mockProvider) Stream(_ context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	resp, err := m.Chat(context.Background(), req)
	if err != nil {
		return nil, err
	}
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{Delta: resp.Content, Done: true}
	close(ch)
	return ch, nil
}

// echoTool is a trivial tool that echoes its input.
type echoTool struct{}

func (e *echoTool) Name() string        { return "echo" }
func (e *echoTool) Description() string { return "Echo input back" }
func (e *echoTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
}
func (e *echoTool) Execute(_ context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct{ Text string }
	json.Unmarshal(params, &p)
	return agent.ToolResult{Content: "echo: " + p.Text}, nil
}

// TestIntegration_SimpleResponse verifies that the agent passes a simple
// message to the LLM and returns the response without tool calls.
func TestIntegration_SimpleResponse(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{Content: "Hello from agent!", StopReason: "end_turn"},
		},
	}

	reg := agent.NewRegistry()
	cfg := agent.AgentConfig{
		ID:            "test",
		SystemPrompt:  "You are helpful.",
		MaxIterations: 5,
		MaxTokens:     1000,
	}
	ctxMgr := agent.NewContextManager(agent.ContextConfig{
		ContextWindow:       10000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 1000,
	})

	a := agent.New(cfg, provider, reg, ctxMgr, testLogger())
	result, err := a.Run(context.Background(), "session-1", []llm.ChatMessage{
		{Role: "user", Content: "Hi"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response != "Hello from agent!" {
		t.Fatalf("want 'Hello from agent!', got %q", result.Response)
	}
	if result.Iterations != 1 {
		t.Fatalf("want 1 iteration, got %d", result.Iterations)
	}
}

// TestIntegration_ToolCall verifies the full ReAct loop:
// LLM calls a tool → tool executes → result fed back → LLM produces final answer.
func TestIntegration_ToolCall(t *testing.T) {
	toolCallJSON, _ := json.Marshal(map[string]any{
		"id":    "tc-1",
		"name":  "echo",
		"input": map[string]any{"text": "hello world"},
	})

	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{
				// First response: tool call
				ToolCalls: []llm.ToolCall{{
					ID:    "tc-1",
					Name:  "echo",
					Input: json.RawMessage(`{"text":"hello world"}`),
				}},
				StopReason: "tool_use",
			},
			{
				// Second response: final answer after seeing tool result
				Content:    "The echo returned: echo: hello world",
				StopReason: "end_turn",
			},
		},
	}
	_ = toolCallJSON

	reg := agent.NewRegistry()
	_ = reg.Register(&echoTool{})

	cfg := agent.AgentConfig{
		ID:            "test",
		SystemPrompt:  "Use tools when needed.",
		MaxIterations: 10,
		MaxTokens:     1000,
	}
	ctxMgr := agent.NewContextManager(agent.ContextConfig{
		ContextWindow:       10000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 1000,
	})

	a := agent.New(cfg, provider, reg, ctxMgr, testLogger())
	result, err := a.Run(context.Background(), "session-2", []llm.ChatMessage{
		{Role: "user", Content: "Echo hello world"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Response, "echo: hello world") {
		t.Fatalf("response should contain tool result, got: %q", result.Response)
	}
	if result.Iterations != 2 {
		t.Fatalf("want 2 iterations, got %d", result.Iterations)
	}
	if result.ToolCalls != 1 {
		t.Fatalf("want 1 tool call, got %d", result.ToolCalls)
	}
}

// TestIntegration_MaxIterations verifies that the agent stops at MaxIterations
// even if the LLM keeps calling tools.
func TestIntegration_MaxIterations(t *testing.T) {
	// Every response is a tool call — forces the agent to hit MaxIterations
	infiniteToolCall := &llm.ChatResponse{
		ToolCalls: []llm.ToolCall{{
			ID:    "tc-loop",
			Name:  "echo",
			Input: json.RawMessage(`{"text":"loop"}`),
		}},
		StopReason: "tool_use",
	}

	responses := make([]*llm.ChatResponse, 20)
	for i := range responses {
		responses[i] = infiniteToolCall
	}
	provider := &mockProvider{responses: responses}

	reg := agent.NewRegistry()
	_ = reg.Register(&echoTool{})

	cfg := agent.AgentConfig{
		ID:            "test",
		SystemPrompt:  "Loop forever.",
		MaxIterations: 3, // small limit
		MaxTokens:     1000,
	}
	ctxMgr := agent.NewContextManager(agent.ContextConfig{
		ContextWindow:       10000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 1000,
	})

	a := agent.New(cfg, provider, reg, ctxMgr, testLogger())
	result, err := a.Run(context.Background(), "session-3", []llm.ChatMessage{
		{Role: "user", Content: "Loop"},
	})

	// Should not error — should stop with max_iterations reason
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "max_iterations" {
		t.Fatalf("want stop_reason=max_iterations, got %q", result.StopReason)
	}
	if result.Iterations > cfg.MaxIterations+1 {
		t.Fatalf("ran %d iterations, limit was %d", result.Iterations, cfg.MaxIterations)
	}
}

// TestIntegration_ContextCancellation verifies the agent respects context cancellation.
func TestIntegration_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{Content: "too late", StopReason: "end_turn"},
		},
	}

	reg := agent.NewRegistry()
	cfg := agent.AgentConfig{
		ID:            "test",
		SystemPrompt:  "You are helpful.",
		MaxIterations: 5,
		MaxTokens:     1000,
	}
	ctxMgr := agent.NewContextManager(agent.ContextConfig{
		ContextWindow:       10000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 1000,
	})

	a := agent.New(cfg, provider, reg, ctxMgr, testLogger())
	_, err := a.Run(ctx, "session-4", []llm.ChatMessage{
		{Role: "user", Content: "respond"},
	})

	if err == nil {
		t.Fatal("want an error from context cancellation, got nil")
	}
}

// TestIntegration_UnknownToolRecovery verifies that the agent handles unknown
// tool calls gracefully without crashing.
func TestIntegration_UnknownToolRecovery(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{{
					ID:    "tc-unknown",
					Name:  "nonexistent_tool",
					Input: json.RawMessage(`{}`),
				}},
				StopReason: "tool_use",
			},
			{Content: "recovered gracefully", StopReason: "end_turn"},
		},
	}

	reg := agent.NewRegistry() // empty — no tools registered

	cfg := agent.AgentConfig{
		ID:            "test",
		SystemPrompt:  "Use tools.",
		MaxIterations: 5,
		MaxTokens:     1000,
	}
	ctxMgr := agent.NewContextManager(agent.ContextConfig{
		ContextWindow:       10000,
		BudgetPct:           0.8,
		MaxToolOutputTokens: 1000,
	})

	a := agent.New(cfg, provider, reg, ctxMgr, testLogger())
	result, err := a.Run(context.Background(), "session-5", []llm.ChatMessage{
		{Role: "user", Content: "use nonexistent tool"},
	})

	if err != nil {
		t.Fatalf("agent should recover from unknown tool, got error: %v", err)
	}
	if result.Response == "" {
		t.Fatal("agent should produce a response after recovery")
	}
}

func testLogger() zerolog.Logger {
	return zerolog.Nop()
}

// Ensure echoTool.Execute works correctly in isolation.
func TestEchoTool(t *testing.T) {
	tool := &echoTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"text":"gostaff"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "echo: gostaff" {
		t.Fatalf("want 'echo: gostaff', got %q", result.Content)
	}
	if result.IsError {
		t.Fatal("should not be an error")
	}
}

// Suppress unused import warning
var _ = fmt.Sprintf
