package skill_test

import (
	"strings"
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

func TestLintSkill_ValidSkill(t *testing.T) {
	input := []byte(`---
name: valid-skill
description: A perfectly valid skill
version: 1.0.0
metadata:
  openclaw:
    requires:
      bins:
        - git
---
# Instructions

Do the thing with git.
`)

	report := skill.LintSkill(input)

	if !report.Valid {
		t.Errorf("expected valid skill, got errors: %v", report.Errors)
	}
	if len(report.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(report.Errors))
	}
	if len(report.Warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d", len(report.Warnings))
	}
}

func TestLintSkill_MissingName(t *testing.T) {
	input := []byte(`---
description: A skill without a name
version: 1.0.0
---
Do stuff.
`)

	report := skill.LintSkill(input)

	if report.Valid {
		t.Error("expected invalid when name is missing")
	}

	hasNameError := false
	for _, e := range report.Errors {
		if strings.Contains(e, "name") {
			hasNameError = true
			break
		}
	}
	if !hasNameError {
		t.Errorf("expected error about missing name, got: %v", report.Errors)
	}
}

func TestLintSkill_MissingDescription(t *testing.T) {
	input := []byte(`---
name: no-desc
version: 1.0.0
---
Instructions.
`)

	report := skill.LintSkill(input)

	if report.Valid {
		t.Error("expected invalid when description is missing")
	}

	hasDescError := false
	for _, e := range report.Errors {
		if strings.Contains(e, "description") {
			hasDescError = true
			break
		}
	}
	if !hasDescError {
		t.Errorf("expected error about missing description, got: %v", report.Errors)
	}
}

func TestLintSkill_EmptyInstructions(t *testing.T) {
	input := []byte(`---
name: empty-body
description: Has no instructions
version: 1.0.0
---
`)

	report := skill.LintSkill(input)

	hasWarning := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "instruction") || strings.Contains(w, "empty") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("expected warning about empty instructions, got: %v", report.Warnings)
	}
}

func TestLintSkill_MalformedYAMLStillReportsIssues(t *testing.T) {
	input := []byte(`---
name: broken
description: [unclosed bracket
---
Some instructions.
`)

	report := skill.LintSkill(input)

	if len(report.Warnings) == 0 {
		t.Error("expected warnings for malformed YAML")
	}
}

func TestLintSkill_FormatReport(t *testing.T) {
	input := []byte(`---
description: Missing name
version: 1.0.0
---
Instructions.
`)

	report := skill.LintSkill(input)
	output := report.Format()

	if !strings.Contains(output, "ERROR") {
		t.Errorf("formatted report should contain ERROR, got:\n%s", output)
	}
}
