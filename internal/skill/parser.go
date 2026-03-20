package skill

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseSkillMD extracts YAML frontmatter and markdown instructions from an
// OpenClaw-compatible SKILL.md file. It is deliberately forgiving: malformed
// YAML produces warnings instead of hard errors, and missing delimiters
// trigger fallback extraction so instructions are never silently lost.
func ParseSkillMD(source []byte) (*ParsedSkill, error) {
	result := &ParsedSkill{
		Warnings: make([]ParseWarning, 0),
	}

	if len(bytes.TrimSpace(source)) == 0 {
		return result, nil
	}

	frontmatter, body, warnings := extractFrontmatter(source)
	result.Warnings = append(result.Warnings, warnings...)

	if len(frontmatter) > 0 {
		if err := yaml.Unmarshal(frontmatter, &result.Manifest); err != nil {
			result.Warnings = append(result.Warnings, ParseWarning{
				Line:    1,
				Message: fmt.Sprintf("malformed YAML frontmatter: %v", err),
			})
		}
	}

	result.Instructions = strings.TrimSpace(string(body))

	// Fallback: if no name was extracted from YAML, try the first # heading
	// in the instructions. ~14% of real ClawHub skills use pure markdown with
	// no frontmatter name field.
	if result.Manifest.Name == "" {
		if heading := extractFirstHeading(result.Instructions); heading != "" {
			result.Manifest.Name = heading
		}
	}

	return result, nil
}

// extractFirstHeading returns the text of the first ATX heading (# Title)
// found in markdown content. Returns empty string if none found.
func extractFirstHeading(md string) string {
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			// Strip common suffixes/decorations
			name = strings.TrimRight(name, " #")
			if name != "" {
				return name
			}
		}
	}
	return ""
}

// extractFrontmatter splits a SKILL.md into YAML frontmatter bytes and
// remaining markdown body bytes. It handles common malformations:
//   - Leading whitespace before the opening ---
//   - Missing opening --- (only closing present)
//   - No delimiters at all
//   - Extra --- in the body (horizontal rules)
func extractFrontmatter(source []byte) (frontmatter []byte, body []byte, warnings []ParseWarning) {
	trimmed := bytes.TrimLeft(source, " \t\r\n")

	// Check if content starts with ---
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		// No opening delimiter. Check if there's a lone --- somewhere that
		// might be a closing delimiter for an implicit frontmatter block.
		idx := bytes.Index(source, []byte("\n---"))
		if idx >= 0 {
			candidate := source[:idx]
			rest := source[idx+4:] // skip \n---

			// Skip trailing newline after ---
			if len(rest) > 0 && rest[0] == '\n' {
				rest = rest[1:]
			}

			warnings = append(warnings, ParseWarning{
				Line:    1,
				Message: "missing opening --- delimiter, attempting frontmatter extraction",
			})
			return bytes.TrimSpace(candidate), rest, warnings
		}

		// No delimiters at all — entire content is instructions
		warnings = append(warnings, ParseWarning{
			Line:    1,
			Message: "no frontmatter delimiters found, treating entire content as instructions",
		})
		return nil, source, warnings
	}

	// We have an opening ---. Find the closing ---.
	// Skip past the opening delimiter line.
	afterOpening := trimmed[3:]
	if len(afterOpening) > 0 && afterOpening[0] == '\n' {
		afterOpening = afterOpening[1:]
	}
	// Also handle \r\n
	if len(afterOpening) > 0 && afterOpening[0] == '\r' {
		afterOpening = afterOpening[1:]
		if len(afterOpening) > 0 && afterOpening[0] == '\n' {
			afterOpening = afterOpening[1:]
		}
	}

	// Find closing --- on its own line
	closingIdx := findClosingDelimiter(afterOpening)
	if closingIdx < 0 {
		// Opening --- but no closing --- : treat everything after opening as
		// frontmatter attempt, with empty body
		warnings = append(warnings, ParseWarning{
			Line:    1,
			Message: "opening --- found but no closing delimiter, treating content as frontmatter",
		})
		return bytes.TrimSpace(afterOpening), nil, warnings
	}

	frontmatter = afterOpening[:closingIdx]
	rest := afterOpening[closingIdx+3:] // skip ---

	// Skip the newline after closing ---
	if len(rest) > 0 && rest[0] == '\r' {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}

	return bytes.TrimSpace(frontmatter), rest, warnings
}

// findClosingDelimiter finds the index of a closing --- that appears at the
// start of a line. Returns -1 if not found.
func findClosingDelimiter(content []byte) int {
	// Check if content starts with ---
	if bytes.HasPrefix(content, []byte("---")) {
		after := content[3:]
		if len(after) == 0 || after[0] == '\n' || after[0] == '\r' {
			return 0
		}
	}

	// Search for \n--- at start of line
	search := content
	offset := 0
	for {
		idx := bytes.Index(search, []byte("\n---"))
		if idx < 0 {
			return -1
		}

		// Check that --- is followed by newline or EOF (not part of longer text)
		afterDash := idx + 4 // \n + ---
		absIdx := offset + idx + 1 // +1 to skip the \n, point at ---

		if afterDash >= len(search) {
			// --- at end of content
			return absIdx
		}

		nextChar := search[afterDash]
		if nextChar == '\n' || nextChar == '\r' {
			return absIdx
		}

		// Not a real delimiter (e.g., ----), keep searching
		search = search[idx+4:]
		offset += idx + 4
	}
}
