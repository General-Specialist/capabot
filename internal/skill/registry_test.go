package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/polymath/gostaff/internal/skill"
)

func TestRegistry_LoadDir_Empty(t *testing.T) {
	dir := t.TempDir()
	reg := skill.NewRegistry()

	n, err := reg.LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 skills, got %d", n)
	}
	if reg.Len() != 0 {
		t.Errorf("expected empty registry")
	}
}

func TestRegistry_LoadDir_NonExistent(t *testing.T) {
	reg := skill.NewRegistry()
	n, err := reg.LoadDir("/nonexistent/path/skills")
	if err != nil {
		t.Fatalf("non-existent dir should not error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestRegistry_LoadDir_LoadsSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "my-skill", `---
name: my-skill
description: Does things
---
You are a helpful skill.`)

	reg := skill.NewRegistry()
	n, err := reg.LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 skill, got %d", n)
	}

	s := reg.Get("my-skill")
	if s == nil {
		t.Fatal("expected skill to be registered")
	}
	if s.Manifest.Description != "Does things" {
		t.Errorf("unexpected description: %q", s.Manifest.Description)
	}
	if s.Instructions == "" {
		t.Error("expected instructions to be set")
	}
}

func TestRegistry_Precedence(t *testing.T) {
	// Workspace dir has higher precedence than user dir.
	workspaceDir := t.TempDir()
	userDir := t.TempDir()

	writeSkill(t, workspaceDir, "shared-skill", `---
name: shared-skill
description: workspace version
---
Workspace instructions.`)

	writeSkill(t, userDir, "shared-skill", `---
name: shared-skill
description: user version
---
User instructions.`)

	reg := skill.NewRegistry()
	reg.LoadDir(workspaceDir) // higher precedence
	reg.LoadDir(userDir)

	s := reg.Get("shared-skill")
	if s == nil {
		t.Fatal("skill not found")
	}
	if s.Manifest.Description != "workspace version" {
		t.Errorf("workspace should win precedence, got %q", s.Manifest.Description)
	}
}

func TestRegistry_SkillNameFallsBackToDir(t *testing.T) {
	dir := t.TempDir()
	// SKILL.md with no name field
	writeSkill(t, dir, "unnamed-dir", "Just instructions, no frontmatter.")

	reg := skill.NewRegistry()
	reg.LoadDir(dir)

	s := reg.Get("unnamed-dir")
	if s == nil {
		t.Fatal("expected skill registered under directory name")
	}
}

func TestRegistry_List(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "skill-a", "---\nname: skill-a\n---\nA")
	writeSkill(t, dir, "skill-b", "---\nname: skill-b\n---\nB")

	reg := skill.NewRegistry()
	reg.LoadDir(dir)

	list := reg.List()
	if len(list) != 2 {
		t.Errorf("expected 2 skills, got %d", len(list))
	}
}

func TestCheckRequirements_NoMeta(t *testing.T) {
	s := &skill.ParsedSkill{}
	missing := skill.CheckRequirements(s)
	if len(missing) != 0 {
		t.Errorf("expected no missing bins, got %v", missing)
	}
}

func TestCheckRequirements_AllPresent(t *testing.T) {
	// "go" should always be on PATH in test environment
	s := &skill.ParsedSkill{
		Manifest: skill.SkillManifest{
			Metadata: skill.SkillMetadata{
				OpenClaw: &skill.SkillMetadataInner{
					Requires: skill.SkillRequires{
						Bins: []string{"go"},
					},
				},
			},
		},
	}
	missing := skill.CheckRequirements(s)
	if len(missing) != 0 {
		t.Errorf("expected no missing bins, got %v", missing)
	}
}

func TestCheckRequirements_Missing(t *testing.T) {
	s := &skill.ParsedSkill{
		Manifest: skill.SkillManifest{
			Metadata: skill.SkillMetadata{
				OpenClaw: &skill.SkillMetadataInner{
					Requires: skill.SkillRequires{
						Bins: []string{"__nonexistent_binary_xyz__"},
					},
				},
			},
		},
	}
	missing := skill.CheckRequirements(s)
	if len(missing) != 1 {
		t.Errorf("expected 1 missing bin, got %v", missing)
	}
}

// writeSkill creates a skill directory with a SKILL.md inside dir.
func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
