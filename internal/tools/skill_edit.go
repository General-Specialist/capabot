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

// SkillEditMarkdownTool lets the agent update a Tier 1 markdown skill's instructions or description.
type SkillEditMarkdownTool struct {
	skillsDir string
	skillReg  *skill.Registry
}

func NewSkillEditMarkdownTool(skillsDir string, skillReg *skill.Registry) *SkillEditMarkdownTool {
	return &SkillEditMarkdownTool{skillsDir: skillsDir, skillReg: skillReg}
}

func (t *SkillEditMarkdownTool) Name() string { return "skill_edit" }
func (t *SkillEditMarkdownTool) Description() string {
	return "Edit an existing markdown skill's instructions or description. Use this to update a Tier 1 skill created with skill_create_markdown."
}
func (t *SkillEditMarkdownTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name":         {"type": "string", "description": "Name of the existing skill to edit"},
			"instructions": {"type": "string", "description": "New markdown instructions (replaces body, keeps frontmatter)"},
			"description":  {"type": "string", "description": "New one-line description"}
		},
		"required": ["name"]
	}`)
}

func (t *SkillEditMarkdownTool) Execute(_ context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Name         string  `json:"name"`
		Instructions *string `json:"instructions"`
		Description  *string `json:"description"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid params", IsError: true}, nil
	}

	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return agent.ToolResult{Content: "name is required", IsError: true}, nil
	}
	if p.Instructions == nil && p.Description == nil {
		return agent.ToolResult{Content: "nothing to update: provide instructions or description", IsError: true}, nil
	}

	skillDir := filepath.Join(t.skillsDir, p.Name)
	skillMDPath := filepath.Join(skillDir, "SKILL.md")

	if _, err := os.Stat(skillMDPath); os.IsNotExist(err) {
		return agent.ToolResult{Content: fmt.Sprintf("skill %q not found", p.Name), IsError: true}, nil
	}

	existing, _ := os.ReadFile(skillMDPath)
	parsed, _ := skill.ParseSkillMD(existing)

	desc, instructions := p.Name, ""
	if parsed != nil {
		desc = parsed.Manifest.Description
		instructions = parsed.Instructions
	}
	if p.Description != nil {
		desc = strings.TrimSpace(*p.Description)
	}
	if p.Instructions != nil {
		instructions = strings.TrimSpace(*p.Instructions)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + p.Name + "\n")
	if desc != "" {
		sb.WriteString("description: " + desc + "\n")
	}
	sb.WriteString("version: 1.0.0\n")
	sb.WriteString("---\n\n")
	sb.WriteString(instructions + "\n")

	if err := os.WriteFile(skillMDPath, []byte(sb.String()), 0o644); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("writing SKILL.md: %v", err), IsError: true}, nil
	}

	if t.skillReg != nil {
		t.skillReg.LoadDir(t.skillsDir) //nolint:errcheck
	}

	return agent.ToolResult{Content: fmt.Sprintf("skill %q updated successfully", p.Name)}, nil
}
