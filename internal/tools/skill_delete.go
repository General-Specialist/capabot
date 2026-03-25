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

// SkillDeleteMarkdownTool lets the agent remove a Tier 1 markdown skill.
type SkillDeleteMarkdownTool struct {
	skillsDir string
	skillReg  *skill.Registry
}

func NewSkillDeleteMarkdownTool(skillsDir string, skillReg *skill.Registry) *SkillDeleteMarkdownTool {
	return &SkillDeleteMarkdownTool{skillsDir: skillsDir, skillReg: skillReg}
}

func (t *SkillDeleteMarkdownTool) Name() string { return "skill_delete" }
func (t *SkillDeleteMarkdownTool) Description() string {
	return "Delete a markdown skill by name. Only removes Tier 1 skills created with skill_create_markdown."
}
func (t *SkillDeleteMarkdownTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Name of the skill to delete"}
		},
		"required": ["name"]
	}`)
}

func (t *SkillDeleteMarkdownTool) Execute(_ context.Context, params json.RawMessage) (agent.ToolResult, error) {
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

	skillDir := filepath.Join(t.skillsDir, p.Name)

	// Ensure it's a T1 markdown skill (has SKILL.md, no main.go)
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); os.IsNotExist(err) {
		return agent.ToolResult{Content: fmt.Sprintf("skill %q not found", p.Name), IsError: true}, nil
	}
	if _, err := os.Stat(filepath.Join(skillDir, "main.go")); err == nil {
		return agent.ToolResult{Content: fmt.Sprintf("%q is a plugin, not a skill — use plugin_delete instead", p.Name), IsError: true}, nil
	}

	// Only allow removing skills inside the managed skills directory.
	absSkillsDir, _ := filepath.Abs(t.skillsDir)
	absSkillDir, _ := filepath.Abs(skillDir)
	if !strings.HasPrefix(absSkillDir, absSkillsDir) {
		return agent.ToolResult{Content: fmt.Sprintf("skill %q is not removable (system skill)", p.Name), IsError: true}, nil
	}

	if err := os.RemoveAll(skillDir); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("removing skill: %v", err), IsError: true}, nil
	}

	if t.skillReg != nil {
		t.skillReg.Unregister(p.Name)
	}

	return agent.ToolResult{Content: fmt.Sprintf("skill %q deleted", p.Name)}, nil
}
