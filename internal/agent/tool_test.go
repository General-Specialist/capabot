package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// stubTool is a minimal Tool implementation for testing.
type stubTool struct {
	name   string
	desc   string
	params json.RawMessage
	fn     func(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

func (s *stubTool) Name() string                { return s.name }
func (s *stubTool) Description() string          { return s.desc }
func (s *stubTool) Parameters() json.RawMessage  { return s.params }
func (s *stubTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	if s.fn != nil {
		return s.fn(ctx, params)
	}
	return ToolResult{Content: "ok"}, nil
}

func newStub(name string) *stubTool {
	return &stubTool{
		name:   name,
		desc:   name + " description",
		params: json.RawMessage(`{"type":"object"}`),
	}
}

func TestRegistry_Register(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(newStub("alpha")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("expected 1 tool, got %d", reg.Len())
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(newStub("alpha"))

	err := reg.Register(newStub("alpha"))
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(newStub("alpha"))

	tool := reg.Get("alpha")
	if tool == nil {
		t.Fatal("expected tool, got nil")
	}
	if tool.Name() != "alpha" {
		t.Fatalf("expected 'alpha', got %q", tool.Name())
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	reg := NewRegistry()

	if reg.Get("nonexistent") != nil {
		t.Fatal("expected nil for missing tool")
	}
}

func TestRegistry_List(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(newStub("alpha"))
	_ = reg.Register(newStub("beta"))
	_ = reg.Register(newStub("gamma"))

	tools := reg.List()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
}

func TestRegistry_Names(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(newStub("gamma"))
	_ = reg.Register(newStub("alpha"))
	_ = reg.Register(newStub("beta"))

	names := reg.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}

	// Names should be sorted
	expected := []string{"alpha", "beta", "gamma"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestRegistry_Execute(t *testing.T) {
	reg := NewRegistry()
	called := false

	tool := &stubTool{
		name:   "echo",
		desc:   "echoes input",
		params: json.RawMessage(`{"type":"object"}`),
		fn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			called = true
			return ToolResult{Content: string(params)}, nil
		},
	}
	_ = reg.Register(tool)

	result, err := reg.Get("echo").Execute(context.Background(), json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected tool function to be called")
	}
	if result.Content != `{"msg":"hi"}` {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

