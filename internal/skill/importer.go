package skill

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SkillTier classifies how a skill executes in Capabot.
const (
	TierMarkdown = 1 // Pure instruction injection (OpenClaw compatible)
	TierNative   = 2 // Native Go tool implementation
	TierPlugin   = 3 // Script-based execution (TS/JS/Python code module skills)
)

// ToolMapping records a single OpenClaw→Capabot tool name translation.
type ToolMapping struct {
	From string
	To   string
}

// ImportResult describes the outcome of importing a single skill.
type ImportResult struct {
	Success     bool
	SkillName   string
	Tier        int
	Errors      []string
	Warnings    []string
	MappedTools  []ToolMapping
	InstallHints []string
	DestPath     string
}

// ImportSkill copies an OpenClaw skill directory into destRoot, validates it,
// checks binary dependencies, and reports tool name mappings. It is forgiving:
// malformed YAML and missing binaries produce warnings, not failures. Only
// truly fatal issues (missing SKILL.md, dest already exists) return errors.
func ImportSkill(srcDir, destRoot string) (*ImportResult, error) {
	result := &ImportResult{
		Success:  true,
		Tier:     TierMarkdown,
		Warnings:     make([]string, 0),
		Errors:       make([]string, 0),
		InstallHints: make([]string, 0),
	}

	// 1. Read and parse SKILL.md (or auto-generate for plugin-only repos)
	skillPath := filepath.Join(srcDir, "SKILL.md")
	source, err := os.ReadFile(skillPath)
	if err != nil {
		// No SKILL.md — check if this is a plugin-only directory
		if detectTier(srcDir) != TierPlugin {
			return nil, fmt.Errorf("cannot read SKILL.md: %w", err)
		}
		// Auto-generate a minimal SKILL.md for plugin repos
		name, desc := inferPluginMeta(srcDir)
		source = []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\nPlugin skill (auto-generated).\n", name, desc))
		// Write it so the imported skill has one
		_ = os.WriteFile(skillPath, source, 0o644)
		result.Warnings = append(result.Warnings, "no SKILL.md found — auto-generated from package.json/directory name")
	}

	parsed, err := ParseSkillMD(source)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SKILL.md: %w", err)
	}

	// Propagate parse warnings
	for _, w := range parsed.Warnings {
		result.Warnings = append(result.Warnings, w.Message)
	}

	// 2. Determine skill name
	result.SkillName = parsed.Manifest.Name
	if result.SkillName == "" {
		result.SkillName = extractNameFallback(source)
	}
	if result.SkillName == "" {
		result.SkillName = filepath.Base(srcDir)
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("no name in frontmatter, using directory name: %s", result.SkillName))
	}

	// 3. Check destination doesn't already exist
	destDir := filepath.Join(destRoot, result.SkillName)
	if _, err := os.Stat(destDir); err == nil {
		return nil, fmt.Errorf("skill %q already exists at %s", result.SkillName, destDir)
	}

	// 4. Detect tier based on code modules
	result.Tier = detectTier(srcDir)
	if result.Tier == TierPlugin {
		// Check that at least one supported runtime is available.
		runtimeFound := false
		for _, entry := range pluginEntryPoints {
			if _, err := os.Stat(filepath.Join(srcDir, entry)); err != nil {
				continue
			}
			rt, ok := pluginRuntimes[entry]
			if !ok {
				continue
			}
			if _, err := exec.LookPath(rt); err == nil {
				runtimeFound = true
				break
			}
		}
		if !runtimeFound {
			result.Warnings = append(result.Warnings,
				"skill contains a code module but no supported runtime (bun, node, python3) was found on PATH")
		}
	}

	// 5. Check runtime dependencies
	checkRequiredBins(parsed, result)
	checkRequiredEnv(parsed, result)
	checkInstallHints(parsed, result)

	// 6. Scan instructions for OpenClaw tool names and build mapping
	scanToolReferences(parsed.Instructions, result)

	// 7. Copy skill files to destination
	if err := copySkillDir(srcDir, destDir); err != nil {
		return nil, fmt.Errorf("failed to copy skill: %w", err)
	}
	result.DestPath = destDir

	// 7b. Auto-install npm dependencies for plugin skills
	if result.Tier == TierPlugin {
		if warn := installNodeDeps(destDir); warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
	}

	// 8. Run lint and propagate any issues
	lintReport := LintSkill(source)
	for _, e := range lintReport.Errors {
		result.Warnings = append(result.Warnings, "lint: "+e)
	}
	for _, w := range lintReport.Warnings {
		result.Warnings = append(result.Warnings, "lint: "+w)
	}

	return result, nil
}

// detectTier checks for code module files that indicate the skill needs
// more than pure markdown instruction injection.
func detectTier(srcDir string) int {
	codeFiles := []string{
		"index.ts", "index.js", "index.py", "index.go",
		"src/index.ts", "src/index.js", "src/index.py",
		"package.json", "clawdbot.plugin.json", "openclaw.plugin.json",
	}
	for _, name := range codeFiles {
		if _, err := os.Stat(filepath.Join(srcDir, name)); err == nil {
			return TierPlugin
		}
	}
	return TierMarkdown
}

// checkRequiredBins verifies that binaries declared in requires.bins exist
// on the host PATH. Missing binaries produce warnings, not errors.
func checkRequiredBins(parsed *ParsedSkill, result *ImportResult) {
	meta := parsed.Manifest.Metadata.Resolved()
	if meta == nil {
		return
	}

	for _, bin := range meta.Requires.Bins {
		if _, err := exec.LookPath(bin); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("required binary %q not found on PATH", bin))
		}
	}

	// anyBins: at least one must exist
	if len(meta.Requires.AnyBins) > 0 {
		found := false
		for _, bin := range meta.Requires.AnyBins {
			if _, err := exec.LookPath(bin); err == nil {
				found = true
				break
			}
		}
		if !found {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("none of the alternative binaries found: %v", meta.Requires.AnyBins))
		}
	}
}

// checkRequiredEnv verifies that env vars declared in requires.env are set.
func checkRequiredEnv(parsed *ParsedSkill, result *ImportResult) {
	meta := parsed.Manifest.Metadata.Resolved()
	if meta == nil {
		return
	}
	for _, env := range meta.Requires.Env {
		if os.Getenv(env) == "" {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("required env var %s is not set", env))
		}
	}
}

// checkInstallHints surfaces install instructions for missing dependencies.
func checkInstallHints(parsed *ParsedSkill, result *ImportResult) {
	meta := parsed.Manifest.Metadata.Resolved()
	if meta == nil {
		return
	}
	for _, spec := range meta.Install {
		missing := false
		for _, bin := range spec.Bins {
			if _, err := exec.LookPath(bin); err != nil {
				missing = true
				break
			}
		}
		if missing {
			hint := formatInstallHint(spec)
			if hint != "" {
				result.InstallHints = append(result.InstallHints, hint)
			}
		}
	}
}

// formatInstallHint produces a human-readable install command from an InstallSpec.
func formatInstallHint(spec InstallSpec) string {
	if spec.Label != "" {
		return spec.Label
	}
	switch spec.Kind {
	case "brew":
		return fmt.Sprintf("brew install %s", spec.Package)
	case "node":
		return fmt.Sprintf("npm install -g %s", spec.Package)
	case "go":
		return fmt.Sprintf("go install %s", spec.Package)
	case "uv":
		return fmt.Sprintf("uv tool install %s", spec.Package)
	default:
		return fmt.Sprintf("install %s (%s)", spec.Package, spec.Kind)
	}
}

// scanToolReferences looks for OpenClaw tool names in the instruction text
// and records any that have Capabot equivalents.
func scanToolReferences(instructions string, result *ImportResult) {
	for openClawName, capabotName := range openClawToCapabot {
		if containsToolReference(instructions, openClawName) {
			result.MappedTools = append(result.MappedTools, ToolMapping{
				From: openClawName,
				To:   capabotName,
			})
		}
	}
}

// containsToolReference checks if a tool name appears in text as a distinct
// word (not as a substring of another word).
func containsToolReference(text, toolName string) bool {
	lower := strings.ToLower(text)
	name := strings.ToLower(toolName)

	idx := 0
	for {
		pos := strings.Index(lower[idx:], name)
		if pos < 0 {
			return false
		}
		absPos := idx + pos
		endPos := absPos + len(name)

		// Check word boundaries
		startOK := absPos == 0 || !isWordChar(lower[absPos-1])
		endOK := endPos >= len(lower) || !isWordChar(lower[endPos])

		if startOK && endOK {
			return true
		}
		idx = absPos + 1
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_'
}

// inferPluginMeta extracts name and description from package.json if present,
// otherwise falls back to the directory name.
func inferPluginMeta(srcDir string) (name, description string) {
	name = filepath.Base(srcDir)
	description = "Plugin installed from GitHub"

	pkgPath := filepath.Join(srcDir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return name, description
	}

	// Minimal JSON extraction — avoid importing encoding/json just for this
	// (it's already imported, but keep it simple)
	type pkgJSON struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	var pkg pkgJSON
	if json.Unmarshal(data, &pkg) == nil {
		if pkg.Name != "" {
			// Strip npm scope prefix (@org/name -> name)
			n := pkg.Name
			if idx := strings.LastIndex(n, "/"); idx >= 0 {
				n = n[idx+1:]
			}
			name = n
		}
		if pkg.Description != "" {
			description = pkg.Description
		}
	}
	return name, description
}

// extractNameFallback does a simple line-by-line scan for "name: <value>"
// in raw source bytes. Used when YAML unmarshal fails entirely but the name
// field is still recoverable as a plain string.
func extractNameFallback(source []byte) string {
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			// Strip quotes if present
			val = strings.Trim(val, `"'`)
			if val != "" {
				return val
			}
		}
	}
	return ""
}

// skipDirs are directories that should never be copied during skill import.
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".github": true,
	"dist": true, "build": true, "__pycache__": true,
}

// copySkillDir recursively copies files from src to dest, creating dest.
// Skips node_modules, .git, and other build artifacts.
func copySkillDir(src, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		destPath := filepath.Join(dest, entry.Name())

		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				continue
			}
			if err := copySkillDir(srcPath, destPath); err != nil {
				return err
			}
			continue
		}

		if err := copyFile(srcPath, destPath); err != nil {
			return fmt.Errorf("copying %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// installNodeDeps runs `bun install` (preferred) or `npm install` in dir if a
// package.json exists. Returns a warning string on failure, empty on success.
func installNodeDeps(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return ""
	}

	// Prefer bun, fall back to npm
	var cmd *exec.Cmd
	if bunPath, err := exec.LookPath("bun"); err == nil {
		cmd = exec.Command(bunPath, "install", "--no-save")
	} else if npmPath, err := exec.LookPath("npm"); err == nil {
		cmd = exec.Command(npmPath, "install", "--no-save")
	} else {
		return "package.json found but neither bun nor npm is on PATH — dependencies not installed"
	}

	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NODE_ENV=production")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("dependency install failed: %s (%s)", err, strings.TrimSpace(string(output)))
	}
	return ""
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
