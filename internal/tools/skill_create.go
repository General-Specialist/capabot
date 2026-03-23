package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/skill"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// SkillCreateTool lets the agent create a new executable native Go skill.
type SkillCreateTool struct {
	skillsDir string
	skillReg  *skill.Registry
	toolReg   *agent.Registry
}

func NewSkillCreateTool(skillsDir string, skillReg *skill.Registry, toolReg *agent.Registry) *SkillCreateTool {
	return &SkillCreateTool{skillsDir: skillsDir, skillReg: skillReg, toolReg: toolReg}
}

func (t *SkillCreateTool) Name() string { return "skill_create" }
func (t *SkillCreateTool) Description() string {
	return "Create a new executable skill from Go source code. The skill is immediately available as a tool. " +
		"The Go program must read JSON params from stdin and write {\"content\":\"...\",\"is_error\":false} to stdout."
}
func (t *SkillCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name":        {"type": "string", "description": "Skill name, lowercase with hyphens (e.g. clear-cache)"},
			"description": {"type": "string", "description": "One-line description of what the skill does"},
			"code":        {"type": "string", "description": "Complete Go source code for package main"}
		},
		"required": ["name", "description", "code"]
	}`)
}

func (t *SkillCreateTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
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
	if p.Description == "" {
		p.Description = "Custom skill: " + p.Name
	}

	if !validName.MatchString(p.Name) {
		return agent.ToolResult{Content: "name must be lowercase alphanumeric with hyphens/underscores", IsError: true}, nil
	}
	if p.Code == "" {
		return agent.ToolResult{Content: "code is required", IsError: true}, nil
	}

	skillDir := filepath.Join(t.skillsDir, p.Name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("creating directory: %v", err), IsError: true}, nil
	}

	skillMD := "---\nname: " + p.Name + "\ndescription: " + p.Description + "\nversion: 1.0.0\n---\n\n" + p.Description + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		os.RemoveAll(skillDir)
		return agent.ToolResult{Content: fmt.Sprintf("writing SKILL.md: %v", err), IsError: true}, nil
	}
	if err := os.WriteFile(filepath.Join(skillDir, "main.go"), []byte(p.Code), 0o644); err != nil {
		os.RemoveAll(skillDir)
		return agent.ToolResult{Content: fmt.Sprintf("writing main.go: %v", err), IsError: true}, nil
	}

	// Compile to catch errors early
	exec, err := skill.NewNativeExecutor(ctx, skillDir)
	if err != nil {
		os.RemoveAll(skillDir)
		return agent.ToolResult{Content: fmt.Sprintf("compilation failed:\n%v", err), IsError: true}, nil
	}
	_ = exec

	// Hot-reload into registries
	if t.skillReg != nil {
		t.skillReg.LoadDir(t.skillsDir) //nolint:errcheck
	}

	return agent.ToolResult{Content: fmt.Sprintf("skill %q created and compiled successfully", p.Name)}, nil
}
