package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/skill"
)

// SkillSearchTool lets the agent search the ClawHub catalog.
type SkillSearchTool struct {
	clawHub *skill.ClawHubClient
}

func NewSkillSearchTool(clawHub *skill.ClawHubClient) *SkillSearchTool {
	return &SkillSearchTool{clawHub: clawHub}
}

func (t *SkillSearchTool) Name() string { return "skill_search" }
func (t *SkillSearchTool) Description() string {
	return "Search the ClawHub skill catalog. Returns matching skills with name, description, download count, and slug for installation."
}
func (t *SkillSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query (e.g. 'git', 'docker', 'testing')"},
			"limit": {"type": "integer", "description": "Max results to return (default 20)"}
		},
		"required": ["query"]
	}`)
}

func (t *SkillSearchTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid params", IsError: true}, nil
	}

	p.Query = strings.TrimSpace(p.Query)
	if p.Query == "" {
		return agent.ToolResult{Content: "query is required", IsError: true}, nil
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}

	results, err := t.clawHub.BrowseSkills(ctx, p.Query, p.Limit, 0)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true}, nil
	}

	if len(results) == 0 {
		return agent.ToolResult{Content: "no skills found"}, nil
	}

	var sb strings.Builder
	for i, r := range results {
		if i >= p.Limit {
			break
		}
		fmt.Fprintf(&sb, "- **%s** (%s)", r.Name, r.Path)
		if r.Description != "" {
			sb.WriteString(": " + r.Description)
		}
		if r.Downloads > 0 {
			fmt.Fprintf(&sb, " [%d downloads]", r.Downloads)
		}
		sb.WriteString("\n")
	}

	return agent.ToolResult{Content: sb.String()}, nil
}
