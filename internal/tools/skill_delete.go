package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/skill"
)

// SkillDeleteTool lets the agent remove an installed skill.
type SkillDeleteTool struct {
	skillsDir string
	skillReg  *skill.Registry
}

func NewSkillDeleteTool(skillsDir string, skillReg *skill.Registry) *SkillDeleteTool {
	return &SkillDeleteTool{skillsDir: skillsDir, skillReg: skillReg}
}

func (t *SkillDeleteTool) Name() string { return "skill_delete" }
func (t *SkillDeleteTool) Description() string {
	return "Delete an installed skill or plugin by name. Only skills in the managed skills directory can be removed."
}
func (t *SkillDeleteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Name of the skill to delete"}
		},
		"required": ["name"]
	}`)
}

func (t *SkillDeleteTool) Execute(_ context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid params", IsError: true}, nil
	}

	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return agent.ToolResult{Content: "name is required", IsError: true}, nil
	}

	if t.skillReg == nil {
		return agent.ToolResult{Content: "skill registry not available", IsError: true}, nil
	}

	skillPath, ok := t.skillReg.SkillPath(p.Name)
	if !ok {
		return agent.ToolResult{Content: fmt.Sprintf("skill %q not found", p.Name), IsError: true}, nil
	}

	// Only allow removing skills inside the managed skills directory.
	absSkillsDir, _ := filepath.Abs(t.skillsDir)
	absSkillPath, _ := filepath.Abs(skillPath)
	if !strings.HasPrefix(absSkillPath, absSkillsDir) {
		return agent.ToolResult{Content: fmt.Sprintf("skill %q is not removable (system or workspace skill)", p.Name), IsError: true}, nil
	}

	if err := os.RemoveAll(skillPath); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("removing skill: %v", err), IsError: true}, nil
	}

	t.skillReg.Unregister(p.Name)

	return agent.ToolResult{Content: fmt.Sprintf("skill %q deleted", p.Name)}, nil
}
