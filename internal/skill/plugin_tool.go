package skill

import (
	"context"
	"encoding/json"
	"fmt"
)

// PluginTool wraps a single tool registered by a PluginProcess.
// Multiple PluginTools can share the same underlying PluginProcess
// (one plugin can register many tools).
type PluginTool struct {
	name        string
	description string
	schema      json.RawMessage
	process     *PluginProcess
}

// NewPluginTool creates a PluginTool for a tool registered by a plugin process.
func NewPluginTool(rt RegisteredTool, proc *PluginProcess) *PluginTool {
	schema := json.RawMessage(`{"type":"object"}`)
	if len(rt.Parameters) > 0 {
		schema = rt.Parameters
	}
	return &PluginTool{
		name:        rt.Name,
		description: rt.Description,
		schema:      schema,
		process:     proc,
	}
}

func (p *PluginTool) Name() string               { return p.name }
func (p *PluginTool) Description() string         { return p.description }
func (p *PluginTool) Parameters() json.RawMessage { return p.schema }

// Run invokes the tool on the long-running plugin subprocess.
func (p *PluginTool) Run(ctx context.Context, params json.RawMessage) (PluginToolResult, error) {
	result, err := p.process.Invoke(ctx, p.name, params)
	if err != nil {
		return PluginToolResult{Content: fmt.Sprintf("plugin error: %v", err), IsError: true}, nil
	}
	return PluginToolResult{Content: result.Content, IsError: result.IsError}, nil
}

// PluginToolResult holds the outcome of a plugin tool execution.
type PluginToolResult struct {
	Content string
	IsError bool
}
