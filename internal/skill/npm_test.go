package skill_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/polymath/gostaff/internal/skill"
)

func TestDownloadNPM_Shellward(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx := context.Background()
	srcDir, err := skill.DownloadNPM(ctx, "shellward")
	if err != nil {
		t.Fatalf("DownloadNPM failed: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(srcDir))

	// Must have openclaw.plugin.json (it's an OpenClaw plugin)
	if _, err := os.Stat(filepath.Join(srcDir, "openclaw.plugin.json")); err != nil {
		t.Error("missing openclaw.plugin.json")
	}

	// Must have package.json
	if _, err := os.Stat(filepath.Join(srcDir, "package.json")); err != nil {
		t.Error("missing package.json")
	}

	// Must have src/ directory
	if info, err := os.Stat(filepath.Join(srcDir, "src")); err != nil || !info.IsDir() {
		t.Error("missing src/ directory")
	}

	// Should be detected as Tier 3 (plugin) by the importer
	destDir := t.TempDir()
	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		t.Fatalf("ImportSkill failed: %v", err)
	}

	if result.SkillName != "shellward" {
		t.Errorf("expected skill name 'shellward', got %q", result.SkillName)
	}
	if result.Tier != 3 {
		t.Errorf("expected tier 3 (plugin), got %d", result.Tier)
	}
	if !result.Success {
		t.Errorf("import reported failure: %v", result.Errors)
	}

	// Verify files were copied to destination
	if _, err := os.Stat(filepath.Join(destDir, "shellward", "openclaw.plugin.json")); err != nil {
		t.Error("openclaw.plugin.json not in destination")
	}
}

func TestDownloadNPM_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	ctx := context.Background()
	_, err := skill.DownloadNPM(ctx, "gostaff-nonexistent-pkg-12345")
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
}
