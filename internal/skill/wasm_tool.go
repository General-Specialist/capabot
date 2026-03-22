package skill

import (
	"context"
	"encoding/json"
	"fmt"
)

// WASMTool wraps a WASMExecutor and adapts it to the agent.Tool interface.
// It is registered in the tool registry like any native Go tool, but delegates
// execution to a sandboxed WASM module.
//
// The schema for the tool is declared in the Skill manifest's frontmatter
// under the "parameters" key. If absent, a generic {"type":"object"} schema
// is used so the LLM can still call it with arbitrary JSON.
type WASMTool struct {
	name        string
	description string
	schema      json.RawMessage
	executor    *WASMExecutor
}

// NewWASMTool creates a WASMTool from a parsed skill and a compiled executor.
func NewWASMTool(s *ParsedSkill, exec *WASMExecutor) *WASMTool {
	schema := json.RawMessage(`{"type":"object"}`)
	if s.Manifest.Parameters != nil {
		schema = s.Manifest.Parameters
	}
	return &WASMTool{
		name:        s.Manifest.Name,
		description: s.Manifest.Description,
		schema:      schema,
		executor:    exec,
	}
}

func (w *WASMTool) Name() string               { return w.name }
func (w *WASMTool) Description() string        { return w.description }
func (w *WASMTool) Parameters() json.RawMessage { return w.schema }

// Run executes the WASM module with params as the input JSON and returns
// a WASMToolResult. Errors from the sandbox are returned as IsError results,
// not Go errors, so the agent loop can feed them back to the LLM gracefully.
func (w *WASMTool) Run(ctx context.Context, params json.RawMessage) (WASMToolResult, error) {
	raw, err := w.executor.Execute(ctx, params)
	if err != nil {
		return WASMToolResult{Content: fmt.Sprintf("wasm error: %v", err), IsError: true}, nil
	}
	result, err := ParseWASMResult(raw)
	if err != nil {
		return WASMToolResult{Content: fmt.Sprintf("wasm result parse error: %v", err), IsError: true}, nil
	}
	return WASMToolResult{Content: result.Content, IsError: result.IsError}, nil
}

// WASMToolResult holds the outcome of a WASM skill execution.
// It mirrors agent.ToolResult without creating an import cycle between
// internal/skill and internal/agent.
type WASMToolResult struct {
	Content string
	IsError bool
}
