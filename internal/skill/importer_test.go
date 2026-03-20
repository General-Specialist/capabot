package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

// helper to create a temp skill directory with files
func createTempSkill(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
	return dir
}

func TestImportSkill_BasicMarkdownOnly(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: hello-world
description: A greeting skill
version: 1.0.0
---
# Instructions

Say hello.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got errors: %v", result.Errors)
	}
	if result.SkillName != "hello-world" {
		t.Errorf("skill name = %q, want %q", result.SkillName, "hello-world")
	}
	if result.Tier != skill.TierMarkdown {
		t.Errorf("tier = %d, want TierMarkdown (%d)", result.Tier, skill.TierMarkdown)
	}

	// Verify SKILL.md was copied
	copied := filepath.Join(destDir, "hello-world", "SKILL.md")
	if _, err := os.Stat(copied); os.IsNotExist(err) {
		t.Error("SKILL.md was not copied to destination")
	}
}

func TestImportSkill_WithMetaJSON(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: slack-notify
description: Send Slack notifications
version: 2.0.0
metadata:
  openclaw:
    requires:
      bins:
        - curl
      env:
        - SLACK_WEBHOOK_URL
    emoji: "💬"
---
# Instructions

Use curl to POST to the Slack webhook URL.
`,
		"_meta.json": `{"owner":"steipete","slug":"slack","displayName":"Slack"}`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got errors: %v", result.Errors)
	}

	// Both files should be copied
	for _, name := range []string{"SKILL.md", "_meta.json"} {
		copied := filepath.Join(destDir, "slack-notify", name)
		if _, err := os.Stat(copied); os.IsNotExist(err) {
			t.Errorf("%s was not copied to destination", name)
		}
	}
}

func TestImportSkill_TypeScriptModuleWarning(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: serper-search
description: Custom search via Serper API
version: 1.0.0
metadata:
  openclaw:
    requires:
      env:
        - SERPER_API_KEY
---
Use the serper_search tool.
`,
		"index.ts": `export default function search() { /* ... */ }`,
		"package.json": `{
  "name": "@clawdbot/serper-search",
  "clawdbot": {"extensions": ["./index.ts"]}
}`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still succeed (markdown instructions are importable)
	if !result.Success {
		t.Errorf("expected success even with TS module, got errors: %v", result.Errors)
	}
	if result.Tier != skill.TierWASM {
		t.Errorf("tier = %d, want TierWASM (%d)", result.Tier, skill.TierWASM)
	}

	// Should warn that TypeScript code modules are not natively executable
	hasCodeWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "TypeScript") || strings.Contains(w, "code module") {
			hasCodeWarning = true
			break
		}
	}
	if !hasCodeWarning {
		t.Errorf("expected warning about TypeScript code module, got: %v", result.Warnings)
	}
}

func TestImportSkill_MissingSkillMD(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"README.md": "Not a skill",
	})

	destDir := t.TempDir()
	_, err := skill.ImportSkill(srcDir, destDir)
	if err == nil {
		t.Error("expected error when SKILL.md is missing")
	}
}

func TestImportSkill_InvalidSkillStillCopies(t *testing.T) {
	// A skill with malformed YAML — should import with warnings, not fail
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: wonky
description: [broken yaml
---
Instructions still work.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warnings for malformed YAML")
	}

	// File should still be copied
	copied := filepath.Join(destDir, "wonky", "SKILL.md")
	if _, err := os.Stat(copied); os.IsNotExist(err) {
		t.Error("SKILL.md should still be copied despite malformed YAML")
	}
}

func TestImportSkill_ChecksBinsOnPATH(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: needs-bins
description: Requires specific binaries
version: 1.0.0
metadata:
  openclaw:
    requires:
      bins:
        - git
        - definitely_not_a_real_binary_xyz123
---
Use git and the other thing.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should succeed (missing bins are warnings, not errors)
	if !result.Success {
		t.Errorf("expected success despite missing bins, got errors: %v", result.Errors)
	}

	hasMissingBinWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "definitely_not_a_real_binary_xyz123") {
			hasMissingBinWarning = true
			break
		}
	}
	if !hasMissingBinWarning {
		t.Errorf("expected warning about missing binary, got: %v", result.Warnings)
	}

	// git should NOT produce a warning (it exists on most systems)
	hasGitWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "git") && strings.Contains(w, "not found") {
			hasGitWarning = true
			break
		}
	}
	if hasGitWarning {
		t.Error("git should be found on PATH, but got a warning about it")
	}
}

func TestImportSkill_ToolNameMapping(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: mapped-tools
description: Has OpenClaw tool references
version: 1.0.0
---
# Instructions

Use the exec tool to run commands.
Then use web_fetch to get a page.
Also use memory_search to recall context.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that mapped tools are reported
	if len(result.MappedTools) == 0 {
		t.Error("expected mapped tools to be populated")
	}

	// Verify specific mappings were detected
	foundExec := false
	for _, m := range result.MappedTools {
		if m.From == "exec" && m.To == "shell_exec" {
			foundExec = true
		}
	}
	if !foundExec {
		t.Errorf("expected exec->shell_exec mapping, got: %v", result.MappedTools)
	}
}

func TestImportSkill_AlreadyExists(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: dupe-skill
description: Will be imported twice
version: 1.0.0
---
Instructions.
`,
	})

	destDir := t.TempDir()

	// First import succeeds
	_, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("first import failed: %v", err)
	}

	// Second import should fail (already exists)
	_, err = skill.ImportSkill(srcDir, destDir)
	if err == nil {
		t.Error("expected error when skill already exists at destination")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

func TestImportSkill_MissingEnvWarning(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: needs-env
description: Requires env vars
version: 1.0.0
metadata:
  openclaw:
    requires:
      env:
        - CAPABOT_TEST_NONEXISTENT_VAR_XYZ
---
Use the var.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasEnvWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "CAPABOT_TEST_NONEXISTENT_VAR_XYZ") {
			hasEnvWarning = true
			break
		}
	}
	if !hasEnvWarning {
		t.Errorf("expected warning about missing env var, got: %v", result.Warnings)
	}
}

func TestImportSkill_AnyBinsNoneSatisfied(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: anybins-skill
description: Needs any of several bins
version: 1.0.0
metadata:
  openclaw:
    requires:
      anyBins:
        - capabot_fake_bin_aaa
        - capabot_fake_bin_bbb
---
Instructions.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "capabot_fake_bin_aaa") || strings.Contains(w, "alternative") || strings.Contains(w, "none") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("expected warning when no anyBins found, got: %v", result.Warnings)
	}
}

func TestImportSkill_InstallHints(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: with-install
description: Has install specs
version: 1.0.0
metadata:
  openclaw:
    requires:
      bins:
        - capabot_fake_ynab_xyz
      env:
        - YNAB_API_KEY
    install:
      - kind: node
        package: "@stephendolan/ynab-cli"
        bins:
          - capabot_fake_ynab_xyz
        label: "Install ynab-cli (npm)"
---
Use ynab to manage your budget.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.InstallHints) == 0 {
		t.Fatal("expected install hints for missing binary")
	}
	foundHint := false
	for _, hint := range result.InstallHints {
		if strings.Contains(hint, "npm") || strings.Contains(hint, "ynab") {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Errorf("expected install hint mentioning npm/ynab, got: %v", result.InstallHints)
	}
}

func TestImportSkill_NameFallbackFromMalformedYAML(t *testing.T) {
	srcDir := createTempSkill(t, map[string]string{
		"SKILL.md": `---
name: salvaged-name
description: [totally broken yaml here
version: [also broken
---
Instructions survive.
`,
	})

	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SkillName != "salvaged-name" {
		t.Errorf("skill name = %q, want %q (should be extracted via fallback)", result.SkillName, "salvaged-name")
	}

	// Verify file landed in correct directory
	copied := filepath.Join(destDir, "salvaged-name", "SKILL.md")
	if _, err := os.Stat(copied); os.IsNotExist(err) {
		t.Error("SKILL.md not copied to correct fallback-named directory")
	}
}
