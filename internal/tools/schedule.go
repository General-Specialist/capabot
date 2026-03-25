package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/polymath/gostaff/internal/agent"
)

// ScheduleTool implements the schedule tool, which waits for a specified
// duration before returning. Useful for chaining time-delayed actions.
type ScheduleTool struct {
	maxDelay time.Duration
}

// NewScheduleTool creates a schedule tool with the given maximum allowed delay.
// If maxDelay is 0, it defaults to 5 minutes.
func NewScheduleTool(maxDelay time.Duration) *ScheduleTool {
	if maxDelay <= 0 {
		maxDelay = 5 * time.Minute
	}
	return &ScheduleTool{maxDelay: maxDelay}
}

func (t *ScheduleTool) Name() string { return "schedule" }
func (t *ScheduleTool) Description() string {
	return "Wait for a specified duration before continuing. Use to introduce a deliberate delay between actions."
}

func (t *ScheduleTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"delay":  {"type": "string", "description": "Duration to wait, e.g. '30s', '2m', '1h'. Max 5m."},
			"reason": {"type": "string", "description": "Optional reason for the delay (for audit purposes)"}
		},
		"required": ["delay"]
	}`)
}

func (t *ScheduleTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Delay  string `json:"delay"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Delay == "" {
		return agent.ToolResult{Content: "delay is required", IsError: true}, nil
	}

	d, err := time.ParseDuration(p.Delay)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("invalid delay %q: %v", p.Delay, err), IsError: true}, nil
	}
	if d <= 0 {
		return agent.ToolResult{Content: "delay must be positive", IsError: true}, nil
	}
	if d > t.maxDelay {
		d = t.maxDelay
	}

	select {
	case <-time.After(d):
		msg := fmt.Sprintf("waited %s", d)
		if p.Reason != "" {
			msg += fmt.Sprintf(" (%s)", p.Reason)
		}
		return agent.ToolResult{Content: msg}, nil
	case <-ctx.Done():
		return agent.ToolResult{Content: "wait cancelled", IsError: true}, nil
	}
}
