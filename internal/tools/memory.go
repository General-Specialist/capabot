package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/memory"
)

// MemoryTool provides persistent key-value memory with store/recall/delete actions.
type MemoryTool struct {
	store *memory.Store
}

func NewMemoryTool(store *memory.Store) *MemoryTool {
	return &MemoryTool{store: store}
}

func (t *MemoryTool) Name() string { return "memory" }
func (t *MemoryTool) Description() string {
	return "Persistent key-value memory. Actions: store (save a key-value pair), recall (get value by key, or list all keys if key is empty), delete (remove a key)."
}

func (t *MemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action":    {"type": "string", "enum": ["store", "recall", "delete"], "description": "Action to perform"},
			"key":       {"type": "string", "description": "Memory key (required for store/delete, optional for recall — empty lists all keys)"},
			"value":     {"type": "string", "description": "Value to store (required for store action)"},
			"tenant_id": {"type": "string", "description": "Namespace for isolation (default: global)"}
		},
		"required": ["action"]
	}`)
}

func (t *MemoryTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Action   string `json:"action"`
		Key      string `json:"key"`
		Value    string `json:"value"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.TenantID == "" {
		p.TenantID = "global"
	}

	switch p.Action {
	case "store":
		if p.Key == "" {
			return agent.ToolResult{Content: "key is required for store", IsError: true}, nil
		}
		entry := memory.MemoryEntry{TenantID: p.TenantID, Key: p.Key, Value: p.Value}
		if err := t.store.StoreMemory(ctx, entry); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("store error: %v", err), IsError: true}, nil
		}
		return agent.ToolResult{Content: fmt.Sprintf("stored memory[%s]", p.Key)}, nil

	case "recall":
		entries, err := t.store.ListMemory(ctx, p.TenantID)
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("recall error: %v", err), IsError: true}, nil
		}
		if p.Key == "" {
			if len(entries) == 0 {
				return agent.ToolResult{Content: "no memory entries found"}, nil
			}
			var sb strings.Builder
			for _, e := range entries {
				fmt.Fprintf(&sb, "- %s\n", e.Key)
			}
			return agent.ToolResult{Content: sb.String()}, nil
		}
		for _, e := range entries {
			if e.Key == p.Key {
				return agent.ToolResult{Content: e.Value}, nil
			}
		}
		return agent.ToolResult{Content: fmt.Sprintf("no memory entry found for key %q", p.Key), IsError: true}, nil

	case "delete":
		if p.Key == "" {
			return agent.ToolResult{Content: "key is required for delete", IsError: true}, nil
		}
		if err := t.store.DeleteMemory(ctx, p.TenantID, p.Key); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("delete error: %v", err), IsError: true}, nil
		}
		return agent.ToolResult{Content: fmt.Sprintf("deleted memory[%s]", p.Key)}, nil

	default:
		return agent.ToolResult{Content: fmt.Sprintf("unknown action %q (use store, recall, or delete)", p.Action), IsError: true}, nil
	}
}
