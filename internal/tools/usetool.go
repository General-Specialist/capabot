package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/polymath/gostaff/internal/agent"
)

// UseToolTool is a meta-tool that proxies calls to extended (lazy-loaded) tools.
// It keeps rarely-used tools out of the main tool list, reducing token cost per request.
type UseToolTool struct {
	registry *agent.Registry
}

func NewUseToolTool(registry *agent.Registry) *UseToolTool {
	return &UseToolTool{registry: registry}
}

func (t *UseToolTool) Name() string { return "use_tool" }

func (t *UseToolTool) Description() string {
	desc := "Run an extended tool not in the main tool list. Available tools:\n"
	desc += t.registry.ExtendedDescriptions()
	return desc
}

func (t *UseToolTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"tool":  {"type": "string", "description": "Name of the extended tool to call"},
			"input": {"type": "object", "description": "Input parameters for the tool"}
		},
		"required": ["tool", "input"]
	}`)
}

func (t *UseToolTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Tool  string          `json:"tool"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Tool == "" {
		return agent.ToolResult{Content: "tool name is required", IsError: true}, nil
	}

	tool := t.registry.Get(p.Tool)
	if tool == nil {
		return agent.ToolResult{Content: fmt.Sprintf("tool %q not found", p.Tool), IsError: true}, nil
	}

	input := p.Input
	if input == nil {
		input = json.RawMessage("{}")
	}

	return tool.Execute(ctx, input)
}
