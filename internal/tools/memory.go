package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/memory"
)

// MemoryStoreTool implements the memory_store tool.
type MemoryStoreTool struct {
	store *memory.Store
}

// NewMemoryStoreTool creates a memory_store tool backed by the given store.
func NewMemoryStoreTool(store *memory.Store) *MemoryStoreTool {
	return &MemoryStoreTool{store: store}
}

func (t *MemoryStoreTool) Name() string { return "memory_store" }
func (t *MemoryStoreTool) Description() string {
	return "Store a key-value pair in persistent memory for later recall."
}

func (t *MemoryStoreTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"key":       {"type": "string", "description": "Unique key for this memory entry"},
			"value":     {"type": "string", "description": "Value to store"},
			"tenant_id": {"type": "string", "description": "Namespace for memory isolation (default: global)"}
		},
		"required": ["key", "value"]
	}`)
}

func (t *MemoryStoreTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Key == "" {
		return agent.ToolResult{Content: "key is required", IsError: true}, nil
	}
	if p.TenantID == "" {
		p.TenantID = "global"
	}

	entry := memory.MemoryEntry{
		TenantID: p.TenantID,
		Key:      p.Key,
		Value:    p.Value,
	}
	if err := t.store.StoreMemory(ctx, entry); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("store error: %v", err), IsError: true}, nil
	}
	return agent.ToolResult{Content: fmt.Sprintf("stored memory[%s]", p.Key)}, nil
}

// MemoryRecallTool implements the memory_recall tool.
type MemoryRecallTool struct {
	store *memory.Store
}

// NewMemoryRecallTool creates a memory_recall tool backed by the given store.
func NewMemoryRecallTool(store *memory.Store) *MemoryRecallTool {
	return &MemoryRecallTool{store: store}
}

func (t *MemoryRecallTool) Name() string { return "memory_recall" }
func (t *MemoryRecallTool) Description() string {
	return "Recall a previously stored memory entry by exact key, or list all keys for a tenant."
}

func (t *MemoryRecallTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"key":       {"type": "string", "description": "Key to recall (empty = list all keys)"},
			"tenant_id": {"type": "string", "description": "Namespace used when storing (default: global)"}
		}
	}`)
}

func (t *MemoryRecallTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Key      string `json:"key"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.TenantID == "" {
		p.TenantID = "global"
	}

	entries, err := t.store.ListMemory(ctx, p.TenantID)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("recall error: %v", err), IsError: true}, nil
	}

	if p.Key == "" {
		// List all keys
		if len(entries) == 0 {
			return agent.ToolResult{Content: "no memory entries found"}, nil
		}
		var sb strings.Builder
		for _, e := range entries {
			fmt.Fprintf(&sb, "- %s\n", e.Key)
		}
		return agent.ToolResult{Content: sb.String()}, nil
	}

	// Find exact key
	for _, e := range entries {
		if e.Key == p.Key {
			return agent.ToolResult{Content: e.Value}, nil
		}
	}
	return agent.ToolResult{Content: fmt.Sprintf("no memory entry found for key %q", p.Key), IsError: true}, nil
}

// MemoryDeleteTool implements the memory_delete tool.
type MemoryDeleteTool struct {
	store *memory.Store
}

// NewMemoryDeleteTool creates a memory_delete tool backed by the given store.
func NewMemoryDeleteTool(store *memory.Store) *MemoryDeleteTool {
	return &MemoryDeleteTool{store: store}
}

func (t *MemoryDeleteTool) Name() string { return "memory_delete" }
func (t *MemoryDeleteTool) Description() string {
	return "Delete a memory entry by key."
}

func (t *MemoryDeleteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"key":       {"type": "string", "description": "Key to delete"},
			"tenant_id": {"type": "string", "description": "Namespace used when storing (default: global)"}
		},
		"required": ["key"]
	}`)
}

func (t *MemoryDeleteTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Key      string `json:"key"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Key == "" {
		return agent.ToolResult{Content: "key is required", IsError: true}, nil
	}
	if p.TenantID == "" {
		p.TenantID = "global"
	}

	if err := t.store.DeleteMemory(ctx, p.TenantID, p.Key); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("delete error: %v", err), IsError: true}, nil
	}
	return agent.ToolResult{Content: fmt.Sprintf("deleted memory[%s]", p.Key)}, nil
}
