package skill

import (
	"fmt"
	"strings"
)

// LintReport contains the results of linting a SKILL.md file.
type LintReport struct {
	Valid    bool
	Errors   []string
	Warnings []string
}

// Format returns a human-readable string representation of the lint report.
func (r LintReport) Format() string {
	var b strings.Builder

	for _, e := range r.Errors {
		fmt.Fprintf(&b, "  ERROR: %s\n", e)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  WARN:  %s\n", w)
	}

	if r.Valid {
		b.WriteString("  OK\n")
	}

	return b.String()
}

// LintSkill parses a SKILL.md and validates it against OpenClaw's expected
// schema. Returns a report with errors (invalid) and warnings (degraded but usable).
func LintSkill(source []byte) LintReport {
	report := LintReport{Valid: true}

	parsed, err := ParseSkillMD(source)
	if err != nil {
		report.Valid = false
		report.Errors = append(report.Errors, fmt.Sprintf("parse failure: %v", err))
		return report
	}

	// Propagate parser warnings
	for _, w := range parsed.Warnings {
		report.Warnings = append(report.Warnings, w.Message)
	}

	// Required fields
	if parsed.Manifest.Name == "" {
		report.Valid = false
		report.Errors = append(report.Errors, "missing required field: name")
	}
	if parsed.Manifest.Description == "" {
		report.Valid = false
		report.Errors = append(report.Errors, "missing required field: description")
	}

	// Warnings for optional but recommended fields
	if parsed.Instructions == "" {
		report.Warnings = append(report.Warnings, "skill has empty instructions body")
	}
	if parsed.Manifest.Version == "" {
		report.Warnings = append(report.Warnings, "missing recommended field: version")
	}

	// Check requires.bins against the host PATH
	missingBins := CheckRequirements(parsed)
	for _, bin := range missingBins {
		report.Warnings = append(report.Warnings, fmt.Sprintf("requires binary not found in PATH: %s", bin))
	}

	return report
}
