package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/skill"
)

type createSkillInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Code        string          `json:"code"`
}

var validSkillName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func (s *Server) handleSkillsCreate(w http.ResponseWriter, r *http.Request) {
	var inp createSkillInput
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	inp.Name = strings.TrimSpace(inp.Name)
	inp.Description = strings.TrimSpace(inp.Description)
	inp.Code = strings.TrimSpace(inp.Code)

	if inp.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if !validSkillName.MatchString(inp.Name) {
		writeError(w, "name must be lowercase alphanumeric with hyphens/underscores", http.StatusBadRequest)
		return
	}
	if inp.Code == "" {
		writeError(w, "code is required", http.StatusBadRequest)
		return
	}
	if inp.Description == "" {
		inp.Description = "Custom skill: " + inp.Name
	}

	skillDir := filepath.Join(s.skillsDir, inp.Name)

	// Create the skill directory
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		writeError(w, fmt.Sprintf("creating skill directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Write SKILL.md
	skillMD := buildSkillMD(inp.Name, inp.Description, inp.Parameters)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		os.RemoveAll(skillDir)
		writeError(w, fmt.Sprintf("writing SKILL.md: %v", err), http.StatusInternalServerError)
		return
	}

	// Write main.go
	if err := os.WriteFile(filepath.Join(skillDir, "main.go"), []byte(inp.Code), 0o644); err != nil {
		os.RemoveAll(skillDir)
		writeError(w, fmt.Sprintf("writing main.go: %v", err), http.StatusInternalServerError)
		return
	}

	// Compile the skill to verify the code is valid
	exec, err := skill.NewNativeExecutor(r.Context(), skillDir)
	if err != nil {
		// Clean up on compile failure
		os.RemoveAll(skillDir)
		writeError(w, fmt.Sprintf("compilation failed: %v", err), http.StatusBadRequest)
		return
	}
	_ = exec // executor will be created fresh when registry loads

	// Hot-reload into skill registry
	if s.skillReg != nil {
		s.skillReg.LoadDir(s.skillsDir) //nolint:errcheck
	}

	// Register as a callable tool immediately
	s.registerNewNativeSkill(r.Context(), inp.Name)

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"name":    inp.Name,
		"success": true,
		"tier":    2,
	})
}

// registerNewNativeSkill compiles and registers a single native skill into the tool registry.
func (s *Server) registerNewNativeSkill(ctx context.Context, name string) {
	if s.skillReg == nil || s.toolReg == nil {
		return
	}
	skillDir, ok := s.skillReg.NativePath(name)
	if !ok {
		return
	}
	parsed := s.skillReg.Get(name)
	if parsed == nil {
		return
	}

	exec, err := skill.NewNativeExecutor(ctx, skillDir)
	if err != nil {
		s.logger.Error().Err(err).Str("skill", name).Msg("failed to compile new native skill")
		return
	}

	nativeTool := skill.NewNativeTool(parsed, exec)
	// Use the adapter that bridges skill.NativeTool → agent.Tool
	if err := s.toolReg.Register(&nativeToolBridge{inner: nativeTool}); err != nil {
		s.logger.Error().Err(err).Str("skill", name).Msg("failed to register new native skill")
		return
	}

	s.logger.Info().Str("skill", name).Msg("custom native skill created and registered")
}

// nativeToolBridge adapts skill.NativeTool to agent.Tool (same as nativeAgentTool in serve.go
// but lives here to avoid import cycles since api package can import both skill and agent).
type nativeToolBridge struct {
	inner *skill.NativeTool
}

func (n *nativeToolBridge) Name() string                { return n.inner.Name() }
func (n *nativeToolBridge) Description() string         { return n.inner.Description() }
func (n *nativeToolBridge) Parameters() json.RawMessage { return n.inner.Parameters() }
func (n *nativeToolBridge) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	res, err := n.inner.Run(ctx, params)
	return agent.ToolResult{Content: res.Content, IsError: res.IsError}, err
}

// handleSkillGet returns the source files for a single skill.
func (s *Server) handleSkillGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	parsed := s.skillReg.Get(name)
	if parsed == nil {
		writeError(w, "skill not found", http.StatusNotFound)
		return
	}
	skillDir, isNative := s.skillReg.NativePath(name)
	code := ""
	if isNative {
		data, _ := os.ReadFile(filepath.Join(skillDir, "main.go"))
		code = string(data)
	}
	tier := 1
	if isNative {
		tier = 2
	}
	writeJSON(w, map[string]any{
		"name":        parsed.Manifest.Name,
		"description": parsed.Manifest.Description,
		"code":        code,
		"tier":        tier,
	})
}

// handleSkillUpdate overwrites a skill's code/description and recompiles.
func (s *Server) handleSkillUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	var inp struct {
		Description string `json:"description"`
		Code        string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	skillDir, isNative := s.skillReg.NativePath(name)
	if !isNative {
		writeError(w, "only native (Tier 2) skills can be edited via this endpoint", http.StatusBadRequest)
		return
	}

	if inp.Description != "" {
		md := buildSkillMD(name, inp.Description, nil)
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o644); err != nil {
			writeError(w, fmt.Sprintf("writing SKILL.md: %v", err), http.StatusInternalServerError)
			return
		}
	}
	if inp.Code != "" {
		if err := os.WriteFile(filepath.Join(skillDir, "main.go"), []byte(inp.Code), 0o644); err != nil {
			writeError(w, fmt.Sprintf("writing main.go: %v", err), http.StatusInternalServerError)
			return
		}
		_ = os.Remove(filepath.Join(skillDir, "skill.bin"))
		if _, err := skill.NewNativeExecutor(r.Context(), skillDir); err != nil {
			writeError(w, fmt.Sprintf("compilation failed: %v", err), http.StatusUnprocessableEntity)
			return
		}
	}

	s.skillReg.LoadDir(filepath.Dir(skillDir)) //nolint:errcheck
	s.registerNewNativeSkill(r.Context(), name)
	writeJSON(w, map[string]any{"success": true, "name": name})
}

func buildSkillMD(name, description string, parameters json.RawMessage) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + name + "\n")
	sb.WriteString("description: " + description + "\n")
	sb.WriteString("version: 1.0.0\n")
	if len(parameters) > 0 && string(parameters) != "null" {
		sb.WriteString("parameters: ")
		sb.Write(parameters)
		sb.WriteString("\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString("Custom skill: " + name + "\n")
	return sb.String()
}
