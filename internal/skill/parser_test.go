package skill_test

import (
	"strings"
	"testing"

	"github.com/polymath/gostaff/internal/skill"
)

func TestParseSkillMD_StandardFrontmatter(t *testing.T) {
	input := []byte(`---
name: todoist-cli
description: Manage Todoist tasks, projects, and labels from the command line.
version: 1.2.0
metadata:
  openclaw:
    requires:
      env:
        - TODOIST_API_KEY
      bins:
        - curl
    primaryEnv: TODOIST_API_KEY
    emoji: "✅"
    homepage: https://github.com/example/todoist-cli
---
# Instructions

When the user asks to manage tasks, use the Todoist API.

## Adding a task

Use curl to POST to the Todoist REST API.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Manifest.Name != "todoist-cli" {
		t.Errorf("name = %q, want %q", result.Manifest.Name, "todoist-cli")
	}
	if result.Manifest.Version != "1.2.0" {
		t.Errorf("version = %q, want %q", result.Manifest.Version, "1.2.0")
	}

	meta := result.Manifest.Metadata.Resolved()
	if meta == nil {
		t.Fatal("metadata.Resolved() returned nil")
	}
	if meta.PrimaryEnv != "TODOIST_API_KEY" {
		t.Errorf("primaryEnv = %q, want %q", meta.PrimaryEnv, "TODOIST_API_KEY")
	}
	if len(meta.Requires.Env) != 1 || meta.Requires.Env[0] != "TODOIST_API_KEY" {
		t.Errorf("requires.env = %v, want [TODOIST_API_KEY]", meta.Requires.Env)
	}
	if len(meta.Requires.Bins) != 1 || meta.Requires.Bins[0] != "curl" {
		t.Errorf("requires.bins = %v, want [curl]", meta.Requires.Bins)
	}
	if meta.Emoji != "✅" {
		t.Errorf("emoji = %q, want %q", meta.Emoji, "✅")
	}

	if !strings.Contains(result.Instructions, "When the user asks to manage tasks") {
		t.Errorf("instructions missing expected content, got:\n%s", result.Instructions)
	}
	if !strings.Contains(result.Instructions, "## Adding a task") {
		t.Errorf("instructions missing heading, got:\n%s", result.Instructions)
	}

	if len(result.Warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(result.Warnings), result.Warnings)
	}
}

func TestParseSkillMD_ClawdbotAlias(t *testing.T) {
	input := []byte(`---
name: peekaboo
description: Capture and automate macOS UI with the Peekaboo CLI.
metadata:
  clawdbot:
    requires:
      bins:
        - peekaboo
    os:
      - macos
---
Use peekaboo to capture screenshots.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Manifest.Metadata.OpenClaw != nil {
		t.Error("expected openclaw to be nil when clawdbot alias is used")
	}

	meta := result.Manifest.Metadata.Resolved()
	if meta == nil {
		t.Fatal("metadata.Resolved() returned nil for clawdbot alias")
	}
	if len(meta.Requires.Bins) != 1 || meta.Requires.Bins[0] != "peekaboo" {
		t.Errorf("requires.bins = %v, want [peekaboo]", meta.Requires.Bins)
	}
	if len(meta.OS) != 1 || meta.OS[0] != "macos" {
		t.Errorf("os = %v, want [macos]", meta.OS)
	}
}

func TestParseSkillMD_MissingDelimiters(t *testing.T) {
	// No --- delimiters at all — entire content is instructions
	input := []byte(`# My Cool Skill

This skill does stuff without any frontmatter.

## Steps

1. Do the thing
2. Do the other thing
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Name should be extracted from the first # heading as fallback
	if result.Manifest.Name != "My Cool Skill" {
		t.Errorf("name = %q, want %q (heading fallback)", result.Manifest.Name, "My Cool Skill")
	}

	if !strings.Contains(result.Instructions, "This skill does stuff") {
		t.Errorf("instructions missing content, got:\n%s", result.Instructions)
	}

	hasDelimiterWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "delimiter") {
			hasDelimiterWarning = true
			break
		}
	}
	if !hasDelimiterWarning {
		t.Error("expected a warning about missing delimiters")
	}
}

func TestParseSkillMD_MalformedYAML(t *testing.T) {
	// YAML frontmatter with broken syntax
	input := []byte(`---
name: broken-skill
description: This has: a colon in the value without quotes
version: [not closed
metadata:
  openclaw:
    requires:
      bins:
        - git
---
# Instructions

Do the thing.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("parser should not return hard error on malformed YAML, got: %v", err)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warnings for malformed YAML")
	}

	// Instructions should still be extracted even with broken frontmatter
	if !strings.Contains(result.Instructions, "Do the thing") {
		t.Errorf("instructions missing despite bad frontmatter, got:\n%s", result.Instructions)
	}
}

func TestParseSkillMD_OnlyClosingDelimiter(t *testing.T) {
	// Missing opening ---, only closing --- present
	input := []byte(`name: orphan-skill
description: This has no opening delimiter
---
# Instructions

Use this skill carefully.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still extract instructions
	if !strings.Contains(result.Instructions, "Use this skill carefully") {
		t.Errorf("instructions missing, got:\n%s", result.Instructions)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warnings for missing opening delimiter")
	}
}

func TestParseSkillMD_EmptyInput(t *testing.T) {
	result, err := skill.ParseSkillMD([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error on empty input: %v", err)
	}

	if result.Instructions != "" {
		t.Errorf("expected empty instructions, got %q", result.Instructions)
	}
	if result.Manifest.Name != "" {
		t.Errorf("expected empty name, got %q", result.Manifest.Name)
	}
}

func TestParseSkillMD_InstructionsPreserveMarkdown(t *testing.T) {
	input := []byte(`---
name: markdown-test
description: Tests markdown preservation
version: 1.0.0
---
# Main Heading

Some paragraph text.

## Subheading

- List item one
- List item two

` + "```bash\ncurl https://example.com\n```" + `

Another paragraph.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Instructions, "# Main Heading") {
		t.Error("instructions should preserve headings")
	}
	if !strings.Contains(result.Instructions, "- List item one") {
		t.Error("instructions should preserve list items")
	}
	if !strings.Contains(result.Instructions, "curl https://example.com") {
		t.Error("instructions should preserve code blocks")
	}
	if !strings.Contains(result.Instructions, "Another paragraph") {
		t.Error("instructions should preserve trailing paragraphs")
	}
}

func TestParseSkillMD_UserInvocableFlag(t *testing.T) {
	input := []byte(`---
name: internal-skill
description: Not user invocable
user-invocable: false
---
Internal instructions.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Manifest.UserInvocable == nil {
		t.Fatal("user-invocable should not be nil")
	}
	if *result.Manifest.UserInvocable != false {
		t.Error("user-invocable should be false")
	}
}

func TestParseSkillMD_WhitespaceBeforeFirstDelimiter(t *testing.T) {
	// Some skills have blank lines or whitespace before the opening ---
	input := []byte(`

---
name: whitespace-skill
description: Has leading whitespace
version: 1.0.0
---
Instructions here.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Manifest.Name != "whitespace-skill" {
		t.Errorf("name = %q, want %q", result.Manifest.Name, "whitespace-skill")
	}
	if !strings.Contains(result.Instructions, "Instructions here") {
		t.Errorf("instructions missing, got:\n%s", result.Instructions)
	}
}

func TestParseSkillMD_ExtraDelimiters(t *testing.T) {
	// Some skills have --- inside the markdown body (e.g., as horizontal rules)
	input := []byte(`---
name: hr-skill
description: Has horizontal rules in body
version: 1.0.0
---
# Instructions

First section.

---

Second section after horizontal rule.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Manifest.Name != "hr-skill" {
		t.Errorf("name = %q, want %q", result.Manifest.Name, "hr-skill")
	}
	if !strings.Contains(result.Instructions, "First section") {
		t.Error("instructions missing first section")
	}
	if !strings.Contains(result.Instructions, "Second section after horizontal rule") {
		t.Error("instructions should include content after horizontal rule")
	}
}

func TestParseSkillMD_NoMetadata(t *testing.T) {
	input := []byte(`---
name: simple-skill
description: A simple skill with no metadata block
version: 0.1.0
---
Just do the thing.
`)

	result, err := skill.ParseSkillMD(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Manifest.Name != "simple-skill" {
		t.Errorf("name = %q, want %q", result.Manifest.Name, "simple-skill")
	}
	if result.Manifest.Metadata.Resolved() != nil {
		t.Error("expected nil metadata when no metadata block present")
	}
	if !strings.Contains(result.Instructions, "Just do the thing") {
		t.Errorf("instructions missing, got:\n%s", result.Instructions)
	}
}
