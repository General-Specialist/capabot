package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/skill"
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

	// Compile to verify the code is valid
	exec, err := skill.NewNativeExecutor(r.Context(), skillDir)
	if err != nil {
		os.RemoveAll(skillDir)
		writeError(w, fmt.Sprintf("compilation failed: %v", err), http.StatusBadRequest)
		return
	}
	_ = exec

	// Hot-reload into skill registry
	if s.skillReg != nil {
		s.skillReg.LoadDir(s.skillsDir) //nolint:errcheck
	}

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
	if err := s.toolReg.Register(&nativeToolBridge{inner: nativeTool}); err != nil {
		s.logger.Error().Err(err).Str("skill", name).Msg("failed to register new native skill")
		return
	}

	s.logger.Info().Str("skill", name).Msg("custom native skill created and registered")
}

// nativeToolBridge adapts skill.NativeTool to agent.Tool.
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

// handleSkillUpdate overwrites a skill's content and reloads it.
// For Tier 1 (markdown) skills: accepts description and instructions.
// For Tier 2 (native Go) skills: accepts description and code.
func (s *Server) handleSkillUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	var inp struct {
		Description  string `json:"description"`
		Code         string `json:"code"`
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	skillDir, isNative := s.skillReg.NativePath(name)
	if isNative {
		// Tier 2: native Go skill
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
	} else {
		// Tier 1: markdown skill
		skillPath, ok := s.skillReg.SkillPath(name)
		if !ok {
			writeError(w, "skill not found", http.StatusNotFound)
			return
		}
		if inp.Instructions == "" && inp.Description == "" {
			writeError(w, "instructions or description is required", http.StatusBadRequest)
			return
		}

		// Read current SKILL.md to preserve fields not being updated
		currentData, _ := os.ReadFile(filepath.Join(skillPath, "SKILL.md"))
		currentParsed := s.skillReg.Get(name)
		currentDesc := ""
		if currentParsed != nil {
			currentDesc = currentParsed.Manifest.Description
		}

		desc := inp.Description
		if desc == "" {
			desc = currentDesc
		}
		instructions := inp.Instructions
		if instructions == "" {
			// Extract instructions from current SKILL.md (everything after the front matter)
			content := string(currentData)
			if _, after, ok := strings.Cut(content, "---\n"); ok {
				if _, body, ok2 := strings.Cut(after, "---\n"); ok2 {
					instructions = strings.TrimSpace(body)
				}
			}
		}

		var sb strings.Builder
		sb.WriteString("---\n")
		sb.WriteString("name: " + name + "\n")
		sb.WriteString("description: " + desc + "\n")
		sb.WriteString("version: 1.0.0\n")
		sb.WriteString("---\n\n")
		sb.WriteString(instructions + "\n")

		if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), []byte(sb.String()), 0o644); err != nil {
			writeError(w, fmt.Sprintf("writing SKILL.md: %v", err), http.StatusInternalServerError)
			return
		}
		if s.skillReg != nil {
			s.skillReg.ReloadSkill(name) //nolint:errcheck
		}
	}

	writeJSON(w, map[string]any{"success": true, "name": name})
}

func (s *Server) handleSkillsCreateMarkdown(w http.ResponseWriter, r *http.Request) {
	var inp struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	inp.Name = strings.TrimSpace(inp.Name)
	inp.Description = strings.TrimSpace(inp.Description)
	inp.Instructions = strings.TrimSpace(inp.Instructions)

	if inp.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if !validSkillName.MatchString(inp.Name) {
		writeError(w, "name must be lowercase alphanumeric with hyphens/underscores", http.StatusBadRequest)
		return
	}
	if inp.Instructions == "" {
		writeError(w, "instructions are required", http.StatusBadRequest)
		return
	}
	if inp.Description == "" {
		inp.Description = inp.Name
	}

	skillDir := filepath.Join(s.skillsDir, inp.Name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		writeError(w, fmt.Sprintf("creating skill directory: %v", err), http.StatusInternalServerError)
		return
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + inp.Name + "\n")
	sb.WriteString("description: " + inp.Description + "\n")
	sb.WriteString("version: 1.0.0\n")
	sb.WriteString("---\n\n")
	sb.WriteString(inp.Instructions + "\n")

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(sb.String()), 0o644); err != nil {
		os.RemoveAll(skillDir)
		writeError(w, fmt.Sprintf("writing SKILL.md: %v", err), http.StatusInternalServerError)
		return
	}

	if s.skillReg != nil {
		s.skillReg.LoadDir(s.skillsDir) //nolint:errcheck
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"name":    inp.Name,
		"success": true,
		"tier":    1,
	})
}

func (s *Server) handleSkillsCatalog(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 10000
	}

	client := skill.NewClawHubClient(skill.ClawHubConfig{})
	results, err := client.BrowseSkills(r.Context(), query, limit, offset)
	if err != nil {
		writeError(w, fmt.Sprintf("ClawHub error: %v", err), http.StatusBadGateway)
		return
	}
	if results == nil {
		results = []skill.ClawHubSkillEntry{}
	}
	writeJSON(w, results)
}

func (s *Server) handleSkillsInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}

	// Normalize: if user pasted a full GitHub URL, extract owner/repo
	target := strings.TrimSpace(req.Name)
	if shorthand, ok := skill.ParseGitHubURL(target); ok {
		target = shorthand
	}

	var skillPath string
	var cleanup func()

	if skill.IsGitHubShorthand(target) {
		// GitHub install: download tarball, extract, import
		srcDir, err := skill.DownloadGitHub(r.Context(), target)
		if err != nil {
			writeError(w, fmt.Sprintf("GitHub download failed: %v", err), http.StatusBadGateway)
			return
		}
		// srcDir may be inside a parent temp dir — clean the parent
		cleanup = func() { os.RemoveAll(filepath.Dir(srcDir)) }
		skillPath = srcDir
	} else {
		// Bare name: try ClawHub first, fall back to npm
		client := skill.NewClawHubClient(skill.ClawHubConfig{})
		tmpDir, err := os.MkdirTemp("", "gostaff-install-*")
		if err != nil {
			writeError(w, fmt.Sprintf("temp dir: %v", err), http.StatusInternalServerError)
			return
		}
		cleanup = func() { os.RemoveAll(tmpDir) }

		dlPath, dlErr := client.DownloadSkill(r.Context(), target, tmpDir)
		if dlErr == nil {
			skillPath = dlPath
		} else {
			// ClawHub miss — try npm
			npmPath, npmErr := skill.DownloadNPM(r.Context(), target)
			if npmErr != nil {
				cleanup()
				writeError(w, fmt.Sprintf("not found on ClawHub or npm: %v", npmErr), http.StatusBadGateway)
				return
			}
			// npmPath is inside its own temp dir
			oldCleanup := cleanup
			cleanup = func() { oldCleanup(); os.RemoveAll(filepath.Dir(npmPath)) }
			skillPath = npmPath
		}
	}
	defer cleanup()

	result, err := skill.ImportSkill(skillPath, s.skillsDir)
	if err != nil {
		writeError(w, fmt.Sprintf("import failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Hot-reload the skill registry so the agent can use the new skill immediately.
	if s.skillReg != nil {
		s.skillReg.LoadDir(s.skillsDir) //nolint:errcheck
	}

	writeJSON(w, map[string]any{
		"skill_name": result.SkillName,
		"tier":       result.Tier,
		"success":    result.Success,
		"warnings":   result.Warnings,
	})
}

func (s *Server) handleSkillsUninstall(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if s.skillReg == nil {
		writeError(w, "skill registry not available", http.StatusServiceUnavailable)
		return
	}

	skillPath, ok := s.skillReg.SkillPath(name)
	if !ok {
		writeError(w, fmt.Sprintf("skill %q not found", name), http.StatusNotFound)
		return
	}

	// Only allow removing skills that live inside the API-managed skills dir.
	if !strings.HasPrefix(skillPath, s.skillsDir) {
		writeError(w, "skill is not removable (system or workspace skill)", http.StatusForbidden)
		return
	}

	if err := os.RemoveAll(skillPath); err != nil {
		writeError(w, fmt.Sprintf("removing skill: %v", err), http.StatusInternalServerError)
		return
	}

	s.skillReg.Unregister(name)
	writeJSON(w, map[string]any{"success": true, "name": name})
}

// handleSkillConfigGet returns the stored config.json for a plugin skill.
func (s *Server) handleSkillConfigGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, ok := s.skillReg.SkillPath(name)
	if !ok {
		writeError(w, "skill not found", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(filepath.Join(path, "config.json"))
	if err != nil {
		writeJSON(w, map[string]any{})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck
}

// handleSkillConfigSet writes config.json for a plugin skill.
func (s *Server) handleSkillConfigSet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, ok := s.skillReg.SkillPath(name)
	if !ok {
		writeError(w, "skill not found", http.StatusNotFound)
		return
	}
	var config json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		writeError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), config, 0o644); err != nil {
		writeError(w, fmt.Sprintf("writing config: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"success": true})
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
