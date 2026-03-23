package skill

import (
	"context"
	"encoding/json"
	"fmt"
)

// NativeTool wraps a NativeExecutor and adapts it to the same interface as WASMTool.
// It is registered in the tool registry like any other tool, but delegates
// execution to a compiled Go binary subprocess.
type NativeTool struct {
	name        string
	description string
	schema      json.RawMessage
	executor    *NativeExecutor
}

// NewNativeTool creates a NativeTool from a parsed skill and a compiled executor.
func NewNativeTool(s *ParsedSkill, exec *NativeExecutor) *NativeTool {
	schema := json.RawMessage(`{"type":"object"}`)
	if s.Manifest.Parameters != nil {
		schema = s.Manifest.Parameters
	}
	return &NativeTool{
		name:        s.Manifest.Name,
		description: s.Manifest.Description,
		schema:      schema,
		executor:    exec,
	}
}

func (n *NativeTool) Name() string               { return n.name }
func (n *NativeTool) Description() string         { return n.description }
func (n *NativeTool) Parameters() json.RawMessage { return n.schema }

// Run executes the native binary with params as JSON stdin and returns
// a NativeToolResult. Errors from the subprocess are returned as IsError results
// so the agent loop can feed them back to the LLM gracefully.
func (n *NativeTool) Run(ctx context.Context, params json.RawMessage) (NativeToolResult, error) {
	raw, err := n.executor.Execute(ctx, params)
	if err != nil {
		return NativeToolResult{Content: fmt.Sprintf("skill error: %v", err), IsError: true}, nil
	}
	result, err := ParseWASMResult(raw) // same JSON envelope
	if err != nil {
		return NativeToolResult{Content: fmt.Sprintf("skill result parse error: %v", err), IsError: true}, nil
	}
	return NativeToolResult{Content: result.Content, IsError: result.IsError}, nil
}

// NativeToolResult holds the outcome of a native skill execution.
// Same shape as WASMToolResult to keep the adapter pattern consistent.
type NativeToolResult struct {
	Content string
	IsError bool
}
