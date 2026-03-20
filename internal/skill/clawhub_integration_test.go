package skill_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

const clawhubPath = "/tmp/clawhub-skills/skills"

// TestClawHubParse_All runs the parser against every SKILL.md in the ClawHub
// repository. This is the real-world fuzz test: 32,000+ skills written by
// thousands of authors with wildly inconsistent formatting.
//
// The parser must NEVER return a hard error — only warnings. Every skill
// should produce a non-nil ParsedSkill, even if the frontmatter is garbage.
func TestClawHubParse_All(t *testing.T) {
	if _, err := os.Stat(clawhubPath); os.IsNotExist(err) {
		t.Skip("ClawHub skills not cloned — run: gh repo clone openclaw/skills /tmp/clawhub-skills -- --depth 1")
	}

	var (
		total      int
		withName   int
		withDesc   int
		withMeta   int
		withInstr  int
		warnCount  int
		hardFails  int
		failPaths  []string
	)

	err := filepath.WalkDir(clawhubPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable dirs
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() || name != "skill.md" {
			return nil
		}

		total++

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			hardFails++
			failPaths = append(failPaths, fmt.Sprintf("READ FAIL: %s: %v", path, readErr))
			return nil
		}

		result, parseErr := skill.ParseSkillMD(source)
		if parseErr != nil {
			hardFails++
			failPaths = append(failPaths, fmt.Sprintf("PARSE FAIL: %s: %v", path, parseErr))
			return nil
		}

		if result.Manifest.Name != "" {
			withName++
		}
		if result.Manifest.Description != "" {
			withDesc++
		}
		if result.Manifest.Metadata.Resolved() != nil {
			withMeta++
		}
		if result.Instructions != "" {
			withInstr++
		}
		warnCount += len(result.Warnings)

		return nil
	})

	if err != nil {
		t.Fatalf("walk error: %v", err)
	}

	// Report
	t.Logf("=== ClawHub Parser Integration Results ===")
	t.Logf("Total SKILL.md files:    %d", total)
	t.Logf("Parse failures (hard):   %d (%.2f%%)", hardFails, pct(hardFails, total))
	t.Logf("With name:               %d (%.1f%%)", withName, pct(withName, total))
	t.Logf("With description:        %d (%.1f%%)", withDesc, pct(withDesc, total))
	t.Logf("With metadata block:     %d (%.1f%%)", withMeta, pct(withMeta, total))
	t.Logf("With instructions:       %d (%.1f%%)", withInstr, pct(withInstr, total))
	t.Logf("Total warnings:          %d (avg %.1f/skill)", warnCount, float64(warnCount)/float64(total))

	if len(failPaths) > 0 {
		t.Logf("--- First 20 failures ---")
		limit := 20
		if len(failPaths) < limit {
			limit = len(failPaths)
		}
		for _, fp := range failPaths[:limit] {
			t.Logf("  %s", fp)
		}
	}

	// The key assertion: hard parse failures must be under 1%
	failRate := pct(hardFails, total)
	if failRate > 1.0 {
		t.Errorf("parse failure rate %.2f%% exceeds 1%% threshold (%d/%d)", failRate, hardFails, total)
	}

	// At least 90% should have a name extracted
	nameRate := pct(withName, total)
	if nameRate < 90.0 {
		t.Errorf("name extraction rate %.1f%% is below 90%% threshold", nameRate)
	}

	// At least 80% should have instructions
	instrRate := pct(withInstr, total)
	if instrRate < 80.0 {
		t.Errorf("instruction extraction rate %.1f%% is below 80%% threshold", instrRate)
	}
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}
