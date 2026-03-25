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

// SkillCreateMarkdownTool lets the agent create a new Tier 1 markdown skill.
type SkillCreateMarkdownTool struct {
	skillsDir string
	skillReg  *skill.Registry
}

func NewSkillCreateMarkdownTool(skillsDir string, skillReg *skill.Registry) *SkillCreateMarkdownTool {
	return &SkillCreateMarkdownTool{skillsDir: skillsDir, skillReg: skillReg}
}

func (t *SkillCreateMarkdownTool) Name() string { return "skill_create_markdown" }
func (t *SkillCreateMarkdownTool) Description() string {
	return "Create a new markdown skill. These are prompt-only skills — " +
		"markdown instructions injected into the system prompt. No code required. " +
		"This is the DEFAULT choice for creating skills: it appears on the Skills page and works for " +
		"any task the agent can accomplish with its existing tools (browsing, shell, files, etc.). " +
		"Only use skill_create (Go code) when the skill itself needs to compile and run code as a standalone binary."
}
func (t *SkillCreateMarkdownTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name":         {"type": "string", "description": "Skill name, lowercase with hyphens (e.g. tone-of-voice)"},
			"description":  {"type": "string", "description": "One-line description of what the skill does"},
			"instructions": {"type": "string", "description": "Markdown instructions/prompt for the agent"}
		},
		"required": ["name", "instructions"]
	}`)
}

func (t *SkillCreateMarkdownTool) Execute(_ context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid params", IsError: true}, nil
	}

	p.Name = strings.TrimSpace(p.Name)
	p.Instructions = strings.TrimSpace(p.Instructions)
	if p.Description == "" {
		p.Description = p.Name
	}

	if !validName.MatchString(p.Name) {
		return agent.ToolResult{Content: "name must be lowercase alphanumeric with hyphens/underscores", IsError: true}, nil
	}
	if p.Instructions == "" {
		return agent.ToolResult{Content: "instructions are required", IsError: true}, nil
	}

	skillDir := filepath.Join(t.skillsDir, p.Name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("creating directory: %v", err), IsError: true}, nil
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + p.Name + "\n")
	sb.WriteString("description: " + p.Description + "\n")
	sb.WriteString("version: 1.0.0\n")
	sb.WriteString("---\n\n")
	sb.WriteString(p.Instructions + "\n")

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(sb.String()), 0o644); err != nil {
		os.RemoveAll(skillDir)
		return agent.ToolResult{Content: fmt.Sprintf("writing SKILL.md: %v", err), IsError: true}, nil
	}

	if t.skillReg != nil {
		t.skillReg.LoadDir(t.skillsDir) //nolint:errcheck
	}

	return agent.ToolResult{Content: fmt.Sprintf("skill %q created successfully", p.Name)}, nil
}
