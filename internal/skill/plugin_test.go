package skill_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

// TestNewPluginProcess_NoEntryPoint verifies error when no entry point found.
func TestNewPluginProcess_NoEntryPoint(t *testing.T) {
	dir := t.TempDir()
	_, err := skill.NewPluginProcess(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error for empty directory")
	}
}

// TestPluginProcess_RegisterAndInvoke spawns a real plugin that registers a tool
// and verifies the full registration + invocation cycle.
func TestPluginProcess_RegisterAndInvoke(t *testing.T) {
	dir := t.TempDir()

	// Write a plugin that registers a "greet" tool via the OpenClaw-style API.
	plugin := `
export function register(api) {
  api.registerTool({
    name: "greet",
    description: "Greets a person",
    parameters: { type: "object", properties: { name: { type: "string" } } },
    execute: (params) => "hello " + (params.name || "world"),
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}
	// The shim expects index.ts or index.js. Symlink index.js -> index.mjs
	// Actually, just write it as index.js with ESM content — node needs --experimental-vm-modules
	// or we use .mjs. Let's just write index.ts for bun.
	os.Remove(filepath.Join(dir, "index.mjs"))
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping (runtime not available): %v", err)
	}
	defer proc.Close()

	// Verify tool registration
	tools := proc.Tools()
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "greet" {
		t.Errorf("want tool name=greet, got %q", tools[0].Name)
	}
	if tools[0].Description != "Greets a person" {
		t.Errorf("want description='Greets a person', got %q", tools[0].Description)
	}

	// Invoke the tool
	params, _ := json.Marshal(map[string]string{"name": "Alice"})
	result, err := proc.Invoke(ctx, "greet", params)
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if result.Content != "hello Alice" {
		t.Errorf("want content='hello Alice', got %q", result.Content)
	}
	if result.IsError {
		t.Error("expected is_error=false")
	}
}

// TestPluginProcess_MultipleTools verifies a plugin can register multiple tools.
func TestPluginProcess_MultipleTools(t *testing.T) {
	dir := t.TempDir()

	plugin := `
export function register(api) {
  api.registerTool({
    name: "add",
    description: "Adds two numbers",
    parameters: { type: "object" },
    execute: (p) => String(Number(p.a) + Number(p.b)),
  });
  api.registerTool({
    name: "multiply",
    description: "Multiplies two numbers",
    parameters: { type: "object" },
    execute: (p) => String(Number(p.a) * Number(p.b)),
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	tools := proc.Tools()
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}

	// Invoke add
	params, _ := json.Marshal(map[string]int{"a": 3, "b": 4})
	result, err := proc.Invoke(ctx, "add", params)
	if err != nil {
		t.Fatalf("invoke add error: %v", err)
	}
	if result.Content != "7" {
		t.Errorf("want add result='7', got %q", result.Content)
	}

	// Invoke multiply
	result, err = proc.Invoke(ctx, "multiply", params)
	if err != nil {
		t.Fatalf("invoke multiply error: %v", err)
	}
	if result.Content != "12" {
		t.Errorf("want multiply result='12', got %q", result.Content)
	}
}

// TestPluginProcess_InvokeUnknownTool verifies error handling for bad tool name.
func TestPluginProcess_InvokeUnknownTool(t *testing.T) {
	dir := t.TempDir()

	plugin := `
export function register(api) {
  api.registerTool({
    name: "noop",
    description: "Does nothing",
    execute: () => "ok",
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	result, err := proc.Invoke(ctx, "nonexistent", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected graceful error, got Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected is_error=true for unknown tool")
	}
}

// TestPluginTool_Metadata verifies PluginTool wraps registered tool fields.
func TestPluginTool_Metadata(t *testing.T) {
	rt := skill.RegisteredTool{
		Name:        "my-tool",
		Description: "A test tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}

	tool := skill.NewPluginTool(rt, nil)
	if tool.Name() != "my-tool" {
		t.Errorf("want name=%q, got %q", "my-tool", tool.Name())
	}
	if tool.Description() != "A test tool" {
		t.Errorf("want description=%q, got %q", "A test tool", tool.Description())
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected schema type=object, got %v", schema["type"])
	}
}

// TestPluginTool_DefaultSchema verifies fallback to generic schema.
func TestPluginTool_DefaultSchema(t *testing.T) {
	rt := skill.RegisteredTool{
		Name:        "bare",
		Description: "No schema",
	}
	tool := skill.NewPluginTool(rt, nil)
	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("default schema not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected default schema type=object, got %v", schema["type"])
	}
}

// TestParseSkillResult verifies the JSON envelope decoder.
func TestParseSkillResult(t *testing.T) {
	raw := []byte(`{"content":"hello from plugin","is_error":false}`)
	result, err := skill.ParseSkillResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "hello from plugin" {
		t.Errorf("want content=%q, got %q", "hello from plugin", result.Content)
	}
	if result.IsError {
		t.Error("expected is_error=false")
	}
}

// TestParseSkillResult_Error verifies error flag parsing.
func TestParseSkillResult_Error(t *testing.T) {
	raw := []byte(`{"content":"something went wrong","is_error":true}`)
	result, err := skill.ParseSkillResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected is_error=true")
	}
}

// TestPluginProcess_RegisterHook verifies hook registration.
func TestPluginProcess_RegisterHook(t *testing.T) {
	dir := t.TempDir()

	plugin := `
export function register(api) {
  api.registerTool({
    name: "noop",
    description: "placeholder",
    execute: () => "ok",
  });
  api.registerHook({
    event: "pre_tool_use",
    name: "blocker",
    handler: ({ tool, params }) => {
      if (tool === "dangerous") return { allow: false };
      return { allow: true };
    },
  });
  api.registerHook({
    event: "post_tool_use",
    name: "logger",
    handler: ({ tool, result }) => ({ result }),
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	hooks := proc.Hooks()
	if len(hooks) != 2 {
		t.Fatalf("want 2 hooks, got %d", len(hooks))
	}
	if hooks[0].Event != "pre_tool_use" {
		t.Errorf("want hook event=pre_tool_use, got %q", hooks[0].Event)
	}
	if hooks[1].Event != "post_tool_use" {
		t.Errorf("want hook event=post_tool_use, got %q", hooks[1].Event)
	}

	// Test invoking the pre-hook — should allow "noop" but block "dangerous"
	result, err := proc.InvokeHook(ctx, "pre_tool_use", "noop", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("invoke hook error: %v", err)
	}
	if !result.Allow {
		t.Error("expected allow=true for noop tool")
	}

	result, err = proc.InvokeHook(ctx, "pre_tool_use", "dangerous", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("invoke hook error: %v", err)
	}
	if result.Allow {
		t.Error("expected allow=false for dangerous tool")
	}
}

// TestPluginProcess_RegisterHttpRoute verifies HTTP route registration and invocation.
func TestPluginProcess_RegisterHttpRoute(t *testing.T) {
	dir := t.TempDir()

	plugin := `
export function register(api) {
  api.registerHttpRoute({
    method: "GET",
    path: "/api/plugins/test/status",
    handler: (req) => ({
      status: 200,
      body: JSON.stringify({ ok: true, path: req.path }),
    }),
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	routes := proc.Routes()
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}
	if routes[0].Method != "GET" || routes[0].Path != "/api/plugins/test/status" {
		t.Errorf("unexpected route: %+v", routes[0])
	}

	resp, err := proc.InvokeHTTP(ctx, skill.HTTPRequest{
		Method: "GET",
		Path:   "/api/plugins/test/status",
	})
	if err != nil {
		t.Fatalf("invoke HTTP error: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("want status=200, got %d", resp.Status)
	}
	if resp.Body == "" {
		t.Error("expected non-empty body")
	}
}

// TestPluginProcess_RegisterProvider verifies LLM provider registration.
func TestPluginProcess_RegisterProvider(t *testing.T) {
	dir := t.TempDir()

	plugin := `
export function register(api) {
  api.registerProvider({
    name: "test-llm",
    models: ["test-model-1", "test-model-2"],
    chat: async ({ model, messages, system }) => ({
      content: "hello from " + model,
      model: model,
    }),
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	providers := proc.Providers()
	if len(providers) != 1 {
		t.Fatalf("want 1 provider, got %d", len(providers))
	}
	if providers[0].Name != "test-llm" {
		t.Errorf("want provider name=test-llm, got %q", providers[0].Name)
	}

	resp, err := proc.InvokeChat(ctx, skill.ChatRequest{
		Provider: "test-llm",
		Model:    "test-model-1",
		Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`),
	})
	if err != nil {
		t.Fatalf("invoke chat error: %v", err)
	}
	if resp.Content != "hello from test-model-1" {
		t.Errorf("want content='hello from test-model-1', got %q", resp.Content)
	}
}

// TestPluginProcess_DefinePluginEntry verifies that plugins using the OpenClaw
// definePluginEntry import pattern work correctly.
func TestPluginProcess_DefinePluginEntry(t *testing.T) {
	dir := t.TempDir()

	// Write a plugin that uses the OpenClaw definePluginEntry pattern
	plugin := `
import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry";

export default definePluginEntry({
  id: "test-plugin",
  name: "Test Plugin",
  description: "A test plugin using definePluginEntry",
  register(api) {
    api.registerTool({
      name: "echo",
      description: "Echoes input",
      parameters: { type: "object", properties: { text: { type: "string" } } },
      execute: (params) => params.text || "empty",
    });
  },
});
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	tools := proc.Tools()
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("want tool name=echo, got %q", tools[0].Name)
	}

	params, _ := json.Marshal(map[string]string{"text": "hello openclaw"})
	result, err := proc.Invoke(ctx, "echo", params)
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if result.Content != "hello openclaw" {
		t.Errorf("want content='hello openclaw', got %q", result.Content)
	}
}

// TestPluginProcess_OnHookEvent verifies that the on() event API works for tool hooks.
func TestPluginProcess_OnHookEvent(t *testing.T) {
	dir := t.TempDir()

	plugin := `
export function register(api) {
  api.registerTool({
    name: "noop",
    description: "noop",
    execute: () => "ok",
  });

  // Use OpenClaw's on() API with before_tool_call event name
  api.on("before_tool_call", ({ tool, params }) => {
    if (tool === "blocked") return { allow: false };
    return { allow: true };
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	hooks := proc.Hooks()
	if len(hooks) != 1 {
		t.Fatalf("want 1 hook, got %d", len(hooks))
	}
	// on("before_tool_call") should map to "pre_tool_use"
	if hooks[0].Event != "pre_tool_use" {
		t.Errorf("want event=pre_tool_use, got %q", hooks[0].Event)
	}

	result, err := proc.InvokeHook(ctx, "pre_tool_use", "blocked", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("invoke hook error: %v", err)
	}
	if result.Allow {
		t.Error("expected allow=false for blocked tool")
	}
}

// TestPluginProcess_RegisterCommand verifies that registerCommand surfaces as a tool.
func TestPluginProcess_RegisterCommand(t *testing.T) {
	dir := t.TempDir()

	plugin := `
export function register(api) {
  api.registerCommand({
    name: "ping",
    description: "Responds with pong",
    handler: (ctx) => "pong",
  });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	proc, err := skill.NewPluginProcess(ctx, dir)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer proc.Close()

	// registerCommand should surface as a tool named "cmd_ping"
	tools := proc.Tools()
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "cmd_ping" {
		t.Errorf("want tool name=cmd_ping, got %q", tools[0].Name)
	}

	result, err := proc.Invoke(ctx, "cmd_ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if result.Content != "pong" {
		t.Errorf("want content='pong', got %q", result.Content)
	}
}
