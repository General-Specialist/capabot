package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/llm"
	"github.com/polymath/capabot/internal/skill"
	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// mockProvider implements llm.Provider for testing.
// ---------------------------------------------------------------------------

type mockProvider struct {
	name     string
	response string
	err      error
}

func (m *mockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{
		Content:    m.response,
		StopReason: "end_turn",
		Usage:      llm.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{Delta: m.response, Done: true}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Models() []llm.ModelInfo { return nil }
func (m *mockProvider) Name() string             { return m.name }

// ---------------------------------------------------------------------------
// mockTool implements agent.Tool for testing.
// ---------------------------------------------------------------------------

type mockTool struct {
	name   string
	result string
}

func (t *mockTool) Name() string                { return t.name }
func (t *mockTool) Description() string          { return "mock tool: " + t.name }
func (t *mockTool) Parameters() json.RawMessage  { return json.RawMessage(`{"type":"object","properties":{}}`) }
func (t *mockTool) Execute(_ context.Context, _ json.RawMessage) (agent.ToolResult, error) {
	return agent.ToolResult{Content: t.result}, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestOrchestrator(providers map[string]llm.Provider, tools *agent.Registry) *Orchestrator {
	reg := NewRegistry()
	skillReg := skill.NewRegistry()
	logger := zerolog.Nop()
	return New(reg, providers, tools, skillReg, nil, logger)
}

func defaultProviders() map[string]llm.Provider {
	return map[string]llm.Provider{
		"mock": &mockProvider{name: "mock", response: "hello from mock"},
	}
}

func emptyToolRegistry() *agent.Registry {
	return agent.NewRegistry()
}

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegistry_Register_DuplicateID(t *testing.T) {
	reg := NewRegistry()

	cfg := AgentConfig{ID: "agent-1", Name: "Agent One", Provider: "mock"}
	if err := reg.Register(cfg); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	// Second registration with same ID must return an error.
	if err := reg.Register(cfg); err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
}

func TestRegistry_Register_EmptyID(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(AgentConfig{ID: "", Name: "No ID"})
	if err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestRegistry_List_Sorted(t *testing.T) {
	reg := NewRegistry()

	ids := []string{"zebra", "alpha", "mango", "beta"}
	for _, id := range ids {
		if err := reg.Register(AgentConfig{ID: id, Name: id}); err != nil {
			t.Fatalf("Register(%q) failed: %v", id, err)
		}
	}

	list := reg.List()
	if len(list) != len(ids) {
		t.Fatalf("expected %d items, got %d", len(ids), len(list))
	}

	want := []string{"alpha", "beta", "mango", "zebra"}
	for i, cfg := range list {
		if cfg.ID != want[i] {
			t.Errorf("list[%d].ID = %q, want %q", i, cfg.ID, want[i])
		}
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := NewRegistry()
	cfg := AgentConfig{ID: "my-agent", Provider: "anthropic"}
	_ = reg.Register(cfg)

	got, ok := reg.Get("my-agent")
	if !ok {
		t.Fatal("Get returned false for existing agent")
	}
	if got.ID != "my-agent" {
		t.Errorf("got ID %q, want %q", got.ID, "my-agent")
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Fatal("Get returned true for nonexistent agent")
	}
}

func TestRegistry_Len(t *testing.T) {
	reg := NewRegistry()
	if reg.Len() != 0 {
		t.Fatalf("expected 0, got %d", reg.Len())
	}
	_ = reg.Register(AgentConfig{ID: "a"})
	_ = reg.Register(AgentConfig{ID: "b"})
	if reg.Len() != 2 {
		t.Fatalf("expected 2, got %d", reg.Len())
	}
}

// ---------------------------------------------------------------------------
// Orchestrator tests
// ---------------------------------------------------------------------------

func TestOrchestrator_Dispatch_Success(t *testing.T) {
	orch := newTestOrchestrator(defaultProviders(), emptyToolRegistry())
	_ = orch.registry.Register(AgentConfig{
		ID:       "greeter",
		Provider: "mock",
	})

	msgs := []llm.ChatMessage{{Role: "user", Content: "hello"}}
	result, err := orch.Dispatch(context.Background(), "greeter", "session-1", msgs)
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	if result.Response != "hello from mock" {
		t.Errorf("unexpected response: %q", result.Response)
	}
}

func TestOrchestrator_Dispatch_UnknownAgent(t *testing.T) {
	orch := newTestOrchestrator(defaultProviders(), emptyToolRegistry())

	_, err := orch.Dispatch(context.Background(), "nonexistent", "session-1", nil)
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
}

func TestOrchestrator_Dispatch_ProviderFallback(t *testing.T) {
	// Register two providers; agent requests "openai" which doesn't exist.
	// The orchestrator should fall back to whichever provider is available.
	providers := map[string]llm.Provider{
		"anthropic": &mockProvider{name: "anthropic", response: "anthropic response"},
	}
	orch := newTestOrchestrator(providers, emptyToolRegistry())
	_ = orch.registry.Register(AgentConfig{
		ID:       "fallback-agent",
		Provider: "openai", // not registered
	})

	msgs := []llm.ChatMessage{{Role: "user", Content: "test"}}
	result, err := orch.Dispatch(context.Background(), "fallback-agent", "session-2", msgs)
	if err != nil {
		t.Fatalf("Dispatch with fallback failed: %v", err)
	}
	if result.Response != "anthropic response" {
		t.Errorf("unexpected response: %q", result.Response)
	}
}

func TestOrchestrator_Dispatch_NoProviders(t *testing.T) {
	orch := newTestOrchestrator(map[string]llm.Provider{}, emptyToolRegistry())
	_ = orch.registry.Register(AgentConfig{
		ID:       "no-provider-agent",
		Provider: "mock",
	})

	_, err := orch.Dispatch(context.Background(), "no-provider-agent", "session-3", nil)
	if err == nil {
		t.Fatal("expected error with no providers, got nil")
	}
}

func TestOrchestrator_Dispatch_ToolFiltering(t *testing.T) {
	// Register two tools globally; agent only requests one.
	toolReg := agent.NewRegistry()
	echoTool := &mockTool{name: "echo_tool", result: "echo!"}
	otherTool := &mockTool{name: "other_tool", result: "other!"}
	_ = toolReg.Register(echoTool)
	_ = toolReg.Register(otherTool)

	orch := newTestOrchestrator(defaultProviders(), toolReg)
	_ = orch.registry.Register(AgentConfig{
		ID:       "filtered-agent",
		Provider: "mock",
		Tools:    []string{"echo_tool"},
	})

	// Build the agent and inspect its tool registry.
	cfg, _ := orch.registry.Get("filtered-agent")
	builtAgent, err := orch.buildAgent(cfg, "session-4")
	if err != nil {
		t.Fatalf("buildAgent failed: %v", err)
	}
	_ = builtAgent // The agent is built; we check via the orchestrator's internal logic below.

	// Build a filtered registry manually to verify filtering works.
	filteredReg := orch.buildToolRegistry(cfg)
	// Should have echo_tool + spawn_agent (spawn is added separately in buildAgent).
	// buildToolRegistry only adds the requested tools, not spawn_agent.
	// echo_tool should be present.
	if filteredReg.Get("echo_tool") == nil {
		t.Error("echo_tool should be in filtered registry")
	}
	// other_tool should NOT be present.
	if filteredReg.Get("other_tool") != nil {
		t.Error("other_tool should NOT be in filtered registry")
	}
}

func TestOrchestrator_Dispatch_AllToolsWhenEmpty(t *testing.T) {
	toolReg := agent.NewRegistry()
	_ = toolReg.Register(&mockTool{name: "tool_a"})
	_ = toolReg.Register(&mockTool{name: "tool_b"})

	orch := newTestOrchestrator(defaultProviders(), toolReg)
	_ = orch.registry.Register(AgentConfig{
		ID:       "all-tools-agent",
		Provider: "mock",
		Tools:    nil, // empty = all tools
	})

	cfg, _ := orch.registry.Get("all-tools-agent")
	filteredReg := orch.buildToolRegistry(cfg)

	if filteredReg.Get("tool_a") == nil {
		t.Error("tool_a should be in registry when Tools is nil")
	}
	if filteredReg.Get("tool_b") == nil {
		t.Error("tool_b should be in registry when Tools is nil")
	}
}

// ---------------------------------------------------------------------------
// SpawnAgentTool tests
// ---------------------------------------------------------------------------

func TestSpawnAgentTool_Execute(t *testing.T) {
	providers := map[string]llm.Provider{
		"mock": &mockProvider{name: "mock", response: "child agent response"},
	}
	orch := newTestOrchestrator(providers, emptyToolRegistry())
	_ = orch.registry.Register(AgentConfig{
		ID:       "child-agent",
		Provider: "mock",
	})

	spawnTool := newSpawnAgentTool(orch, "parent-session")

	params, _ := json.Marshal(map[string]string{
		"agent_id": "child-agent",
		"task":     "do something useful",
	})

	result, err := spawnTool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned tool error: %s", result.Content)
	}
	if result.Content != "child agent response" {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestSpawnAgentTool_Execute_UnknownAgent(t *testing.T) {
	orch := newTestOrchestrator(defaultProviders(), emptyToolRegistry())
	spawnTool := newSpawnAgentTool(orch, "parent-session")

	params, _ := json.Marshal(map[string]string{
		"agent_id": "does-not-exist",
		"task":     "task",
	})

	result, err := spawnTool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error return: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for unknown agent")
	}
}

func TestSpawnAgentTool_Execute_InvalidParams(t *testing.T) {
	orch := newTestOrchestrator(defaultProviders(), emptyToolRegistry())
	spawnTool := newSpawnAgentTool(orch, "parent-session")

	result, err := spawnTool.Execute(context.Background(), json.RawMessage(`not-json`))
	if err != nil {
		t.Fatalf("unexpected error return: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for invalid JSON params")
	}
}

func TestSpawnAgentTool_Execute_EmptyAgentID(t *testing.T) {
	orch := newTestOrchestrator(defaultProviders(), emptyToolRegistry())
	spawnTool := newSpawnAgentTool(orch, "parent-session")

	params, _ := json.Marshal(map[string]string{
		"agent_id": "",
		"task":     "some task",
	})

	result, err := spawnTool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error return: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for empty agent_id")
	}
}

func TestSpawnAgentTool_Name(t *testing.T) {
	orch := newTestOrchestrator(defaultProviders(), emptyToolRegistry())
	tool := newSpawnAgentTool(orch, "sess")
	if tool.Name() != "spawn_agent" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "spawn_agent")
	}
}

func TestSpawnAgentTool_Parameters_ValidJSON(t *testing.T) {
	orch := newTestOrchestrator(defaultProviders(), emptyToolRegistry())
	tool := newSpawnAgentTool(orch, "sess")

	var schema map[string]interface{}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters() returned invalid JSON: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ensure unused import is used (fmt).
// ---------------------------------------------------------------------------

var _ = fmt.Sprintf
