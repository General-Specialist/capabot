package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/skill"
)

// SkillEditTool lets the agent update an existing skill's code or description.
type SkillEditTool struct {
	skillsDir string
	skillReg  *skill.Registry
}

func NewSkillEditTool(skillsDir string, skillReg *skill.Registry) *SkillEditTool {
	return &SkillEditTool{skillsDir: skillsDir, skillReg: skillReg}
}

func (t *SkillEditTool) Name() string { return "skill_edit" }
func (t *SkillEditTool) Description() string {
	return "Update an existing skill's Go source code or description. Recompiles and hot-reloads automatically."
}
func (t *SkillEditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name":        {"type": "string", "description": "Name of the existing skill to edit"},
			"description": {"type": "string", "description": "New one-line description (optional)"},
			"code":        {"type": "string", "description": "New complete Go source code for package main (optional)"}
		},
		"required": ["name"]
	}`)
}

func (t *SkillEditTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Code        string `json:"code"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid params", IsError: true}, nil
	}

	p.Name = strings.TrimSpace(p.Name)
	p.Code = strings.TrimSpace(p.Code)

	skillDir := filepath.Join(t.skillsDir, p.Name)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return agent.ToolResult{Content: fmt.Sprintf("skill %q not found", p.Name), IsError: true}, nil
	}

	if p.Description != "" {
		skillMD := "---\nname: " + p.Name + "\ndescription: " + p.Description + "\nversion: 1.0.0\n---\n\n" + p.Description + "\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("writing SKILL.md: %v", err), IsError: true}, nil
		}
	}

	if p.Code != "" {
		if err := os.WriteFile(filepath.Join(skillDir, "main.go"), []byte(p.Code), 0o644); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("writing main.go: %v", err), IsError: true}, nil
		}
		// Remove cached binary so it recompiles
		_ = os.Remove(filepath.Join(skillDir, "skill.bin"))
		if _, err := skill.NewNativeExecutor(ctx, skillDir); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("compilation failed:\n%v", err), IsError: true}, nil
		}
	}

	if t.skillReg != nil {
		t.skillReg.LoadDir(t.skillsDir) //nolint:errcheck
	}

	return agent.ToolResult{Content: fmt.Sprintf("skill %q updated successfully", p.Name)}, nil
}
