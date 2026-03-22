package skill_test

import (
	"strings"
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

func TestBuildSystemPrompt_NoSkills(t *testing.T) {
	base := "You are a helpful assistant."
	result := skill.BuildSystemPrompt(base, nil)
	if result != base {
		t.Errorf("expected base unchanged, got %q", result)
	}
}

func TestBuildSystemPrompt_WithSkills(t *testing.T) {
	base := "You are a helpful assistant."
	skills := []*skill.ParsedSkill{
		{
			Manifest:     skill.SkillManifest{Name: "test-skill", Description: "Does testing"},
			Instructions: "Always write tests first.",
		},
	}

	result := skill.BuildSystemPrompt(base, skills)

	if !strings.Contains(result, base) {
		t.Error("result should contain base prompt")
	}
	if !strings.Contains(result, "## Skill: test-skill") {
		t.Error("result should contain skill header")
	}
	if !strings.Contains(result, "Always write tests first.") {
		t.Error("result should contain skill instructions")
	}
	if !strings.Contains(result, "Does testing") {
		t.Error("result should contain skill description")
	}
}

func TestBuildSystemPrompt_SkipEmptyInstructions(t *testing.T) {
	base := "Base prompt."
	skills := []*skill.ParsedSkill{
		{Manifest: skill.SkillManifest{Name: "empty-skill"}, Instructions: ""},
		{Manifest: skill.SkillManifest{Name: "real-skill"}, Instructions: "Real instructions."},
	}

	result := skill.BuildSystemPrompt(base, skills)

	if strings.Contains(result, "empty-skill") {
		t.Error("skill with no instructions should be skipped")
	}
	if !strings.Contains(result, "real-skill") {
		t.Error("skill with instructions should be present")
	}
}

func TestBuildSystemPrompt_MultipleSkills(t *testing.T) {
	base := "Base."
	skills := []*skill.ParsedSkill{
		{Manifest: skill.SkillManifest{Name: "skill-a"}, Instructions: "Instructions A."},
		{Manifest: skill.SkillManifest{Name: "skill-b"}, Instructions: "Instructions B."},
	}

	result := skill.BuildSystemPrompt(base, skills)

	posA := strings.Index(result, "skill-a")
	posB := strings.Index(result, "skill-b")
	if posA < 0 || posB < 0 {
		t.Error("both skills should appear in output")
	}
	if posA > posB {
		t.Error("skills should appear in provided order")
	}
}

func TestActiveSkillsFromNames(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "skill-a", "---\nname: skill-a\n---\nInstructions A.")
	writeSkill(t, dir, "skill-b", "---\nname: skill-b\n---\nInstructions B.")

	reg := skill.NewRegistry()
	reg.LoadDir(dir)

	active := skill.ActiveSkillsFromNames(reg, []string{"skill-a", "missing-skill", "skill-b"})
	if len(active) != 2 {
		t.Errorf("expected 2 active skills (skipping missing), got %d", len(active))
	}
	if active[0].Manifest.Name != "skill-a" {
		t.Errorf("expected skill-a first, got %q", active[0].Manifest.Name)
	}
}
