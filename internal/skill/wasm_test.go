package skill_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

// TestWASMTool_NewWASMTool verifies that NewWASMTool correctly reads the
// skill manifest fields into the WASMTool wrapper without needing a real
// WASM binary (executor is nil here since we only test metadata).
func TestWASMTool_NewWASMTool(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	s := &skill.ParsedSkill{
		Manifest: skill.SkillManifest{
			Name:        "my-wasm-skill",
			Description: "A WASM test skill",
			Parameters:  params,
		},
		Instructions: "Do something useful.",
	}

	tool := skill.NewWASMTool(s, nil) // nil executor — we only test metadata
	if tool.Name() != "my-wasm-skill" {
		t.Errorf("want name=%q, got %q", "my-wasm-skill", tool.Name())
	}
	if tool.Description() != "A WASM test skill" {
		t.Errorf("want description=%q, got %q", "A WASM test skill", tool.Description())
	}

	var schemaObj map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schemaObj); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if schemaObj["type"] != "object" {
		t.Errorf("expected schema type=object, got %v", schemaObj["type"])
	}
}

// TestWASMTool_DefaultSchema verifies that when no Parameters field is present
// in the manifest, the tool falls back to a generic {"type":"object"} schema.
func TestWASMTool_DefaultSchema(t *testing.T) {
	s := &skill.ParsedSkill{
		Manifest: skill.SkillManifest{
			Name:        "bare-skill",
			Description: "No schema declared",
		},
	}
	tool := skill.NewWASMTool(s, nil)
	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("default schema not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected default schema type=object, got %v", schema["type"])
	}
}

// TestParseWASMResult verifies the JSON envelope decoder.
func TestParseWASMResult(t *testing.T) {
	raw := []byte(`{"content":"hello from wasm","is_error":false}`)
	result, err := skill.ParseWASMResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "hello from wasm" {
		t.Errorf("want content=%q, got %q", "hello from wasm", result.Content)
	}
	if result.IsError {
		t.Error("expected is_error=false")
	}
}

// TestParseWASMResult_Error verifies error flag parsing.
func TestParseWASMResult_Error(t *testing.T) {
	raw := []byte(`{"content":"something went wrong","is_error":true}`)
	result, err := skill.ParseWASMResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected is_error=true")
	}
}

// TestNewWASMExecutor_InvalidBytes verifies that a garbage binary fails at
// compile time, not at runtime.
func TestNewWASMExecutor_InvalidBytes(t *testing.T) {
	ctx := context.Background()
	_, err := skill.NewWASMExecutor(ctx, []byte("this is not wasm"))
	if err == nil {
		t.Fatal("expected error for invalid wasm bytes, got nil")
	}
}
