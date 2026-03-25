package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/llm"
)

// SpawnAgentTool is a built-in tool that lets a parent agent delegate subtasks
// to peer agents managed by the same Orchestrator.
type SpawnAgentTool struct {
	orch      *Orchestrator
	sessionID string
}

// newSpawnAgentTool creates a new SpawnAgentTool bound to the given orchestrator
// and parent session ID.
func newSpawnAgentTool(orch *Orchestrator, sessionID string) *SpawnAgentTool {
	return &SpawnAgentTool{
		orch:      orch,
		sessionID: sessionID,
	}
}

// Name returns the tool's unique identifier.
func (t *SpawnAgentTool) Name() string { return "spawn_agent" }

// Description returns a human-readable description for the LLM.
func (t *SpawnAgentTool) Description() string {
	return "Delegate a subtask to another agent. Returns that agent's response."
}

// Parameters returns the JSON Schema describing the tool's input parameters.
func (t *SpawnAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"agent_id": {
				"type": "string",
				"description": "ID of the agent to delegate to"
			},
			"task": {
				"type": "string",
				"description": "Task or question for the agent"
			}
		},
		"required": ["agent_id", "task"]
	}`)
}

// spawnParams holds the decoded parameters for a spawn_agent call.
type spawnParams struct {
	AgentID string `json:"agent_id"`
	Task    string `json:"task"`
}

// Execute delegates the task to the named peer agent and returns its response.
func (t *SpawnAgentTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p spawnParams
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{
			IsError: true,
			Content: fmt.Sprintf("orchestrator: invalid spawn_agent params: %s", err.Error()),
		}, nil
	}

	if p.AgentID == "" {
		return agent.ToolResult{
			IsError: true,
			Content: "orchestrator: spawn_agent requires a non-empty agent_id",
		}, nil
	}

	if p.Task == "" {
		return agent.ToolResult{
			IsError: true,
			Content: "orchestrator: spawn_agent requires a non-empty task",
		}, nil
	}

	// Scope child session under the parent session.
	childSessionID := t.sessionID + "/" + p.AgentID

	messages := []llm.ChatMessage{
		{Role: "user", Content: p.Task},
	}

	result, err := t.orch.Dispatch(ctx, p.AgentID, childSessionID, messages)
	if err != nil {
		return agent.ToolResult{
			IsError: true,
			Content: err.Error(),
		}, nil
	}

	return agent.ToolResult{
		Content: result.Response,
	}, nil
}
