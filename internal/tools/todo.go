package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/polymath/gostaff/internal/agent"
)

// TodoItem represents a single task.
type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`   // pending, in_progress, completed, cancelled
	Priority string `json:"priority"` // high, medium, low
}

// TodoTool implements the todo tool — session-scoped task tracking.
// The agent manages todo lists by providing a list_id (defaults to "default").
type TodoTool struct {
	mu    sync.Mutex
	lists map[string][]TodoItem
}

func NewTodoTool() *TodoTool {
	return &TodoTool{lists: make(map[string][]TodoItem)}
}

func (t *TodoTool) Name() string { return "todo" }
func (t *TodoTool) Description() string {
	return "Read or write a task list. Call with todos to replace the list; call without todos to read the current list. Statuses: pending, in_progress, completed, cancelled. Priorities: high, medium, low."
}

func (t *TodoTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"todos": {
				"type": "array",
				"description": "New task list (replaces existing). Omit to read current list.",
				"items": {
					"type": "object",
					"properties": {
						"id":       {"type": "string"},
						"content":  {"type": "string"},
						"status":   {"type": "string", "enum": ["pending", "in_progress", "completed", "cancelled"]},
						"priority": {"type": "string", "enum": ["high", "medium", "low"]}
					},
					"required": ["id", "content", "status", "priority"]
				}
			},
			"list_id": {"type": "string", "description": "List identifier (default: \"default\")"}
		}
	}`)
}

func (t *TodoTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Todos  []TodoItem `json:"todos"`
		ListID string     `json:"list_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.ListID == "" {
		p.ListID = "default"
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if p.Todos != nil {
		// Validate statuses and priorities
		for _, item := range p.Todos {
			if !validStatus(item.Status) {
				return agent.ToolResult{Content: fmt.Sprintf("invalid status %q for item %q", item.Status, item.ID), IsError: true}, nil
			}
			if !validPriority(item.Priority) {
				return agent.ToolResult{Content: fmt.Sprintf("invalid priority %q for item %q", item.Priority, item.ID), IsError: true}, nil
			}
		}
		t.lists[p.ListID] = p.Todos
	}

	items := t.lists[p.ListID]
	if len(items) == 0 {
		return agent.ToolResult{Content: "no tasks"}, nil
	}

	var sb strings.Builder
	for _, item := range items {
		statusIcon := statusIcon(item.Status)
		priorityMarker := ""
		if item.Priority == "high" {
			priorityMarker = " [high]"
		}
		fmt.Fprintf(&sb, "%s [%s]%s %s\n", statusIcon, item.ID, priorityMarker, item.Content)
	}
	return agent.ToolResult{Content: sb.String()}, nil
}

func validStatus(s string) bool {
	switch s {
	case "pending", "in_progress", "completed", "cancelled":
		return true
	}
	return false
}

func validPriority(s string) bool {
	switch s {
	case "high", "medium", "low":
		return true
	}
	return false
}

func statusIcon(status string) string {
	switch status {
	case "completed":
		return "✓"
	case "in_progress":
		return "→"
	case "cancelled":
		return "✗"
	default:
		return "○"
	}
}
