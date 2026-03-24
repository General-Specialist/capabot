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

// SkillEditTool lets the agent update an existing skill's SKILL.md instructions or Go source code.
type SkillEditTool struct {
	skillsDir string
	skillReg  *skill.Registry
}

func NewSkillEditTool(skillsDir string, skillReg *skill.Registry) *SkillEditTool {
	return &SkillEditTool{skillsDir: skillsDir, skillReg: skillReg}
}

func (t *SkillEditTool) Name() string { return "skill_edit" }
func (t *SkillEditTool) Description() string {
	return "Edit an existing skill. Use this to update a skill's SKILL.md instructions/prompt, its one-line description, or its Go source code. Always prefer this over file_write for editing skills."
}
func (t *SkillEditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name":         {"type": "string", "description": "Name of the existing skill to edit"},
			"instructions": {"type": "string", "description": "New markdown instructions/prompt body for SKILL.md (replaces the body, keeps frontmatter)"},
			"description":  {"type": "string", "description": "New one-line description (updates frontmatter only)"},
			"code":         {"type": "string", "description": "New complete Go source code for package main (optional, only for WASM skills)"}
		},
		"required": ["name"]
	}`)
}

func (t *SkillEditTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Name         string  `json:"name"`
		Instructions *string `json:"instructions"` // nil = not provided, "" = clear
		Description  *string `json:"description"`  // nil = not provided, "" = clear
		Code         *string `json:"code"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid params", IsError: true}, nil
	}

	p.Name = strings.TrimSpace(p.Name)
	skillDir := filepath.Join(t.skillsDir, p.Name)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return agent.ToolResult{Content: fmt.Sprintf("skill %q not found", p.Name), IsError: true}, nil
	}

	skillMDPath := filepath.Join(skillDir, "SKILL.md")

	if p.Instructions != nil || p.Description != nil {
		// Parse existing SKILL.md to preserve fields that weren't provided
		existing, _ := os.ReadFile(skillMDPath)
		parsed, _ := skill.ParseSkillMD(existing)

		name := p.Name
		var desc, instructions string
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
		sb.WriteString("name: " + name + "\n")
		if desc != "" {
			sb.WriteString("description: " + desc + "\n")
		}
		sb.WriteString("version: 1.0.0\n")
		sb.WriteString("---\n\n")
		sb.WriteString(instructions)
		sb.WriteString("\n")

		if err := os.WriteFile(skillMDPath, []byte(sb.String()), 0o644); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("writing SKILL.md: %v", err), IsError: true}, nil
		}
	}

	if p.Code != nil {
		code := strings.TrimSpace(*p.Code)
		if err := os.WriteFile(filepath.Join(skillDir, "main.go"), []byte(code), 0o644); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("writing main.go: %v", err), IsError: true}, nil
		}
		_ = os.Remove(filepath.Join(skillDir, "skill.bin"))
		if _, err := skill.NewNativeExecutor(ctx, skillDir); err != nil { //nolint:contextcheck
			return agent.ToolResult{Content: fmt.Sprintf("compilation failed:\n%v", err), IsError: true}, nil
		}
	}

	if t.skillReg != nil {
		t.skillReg.LoadDir(t.skillsDir) //nolint:errcheck
	}

	return agent.ToolResult{Content: fmt.Sprintf("skill %q updated successfully", p.Name)}, nil
}
