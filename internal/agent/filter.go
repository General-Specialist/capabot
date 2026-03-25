package agent

import (
	"strings"
	"unicode"
)

// FilterResult describes what a content filter found.
type FilterResult struct {
	Blocked bool
	Reason  string
}

// ContentFilter scans user messages for prompt injection patterns.
// It is intentionally conservative — blocking known attack patterns
// rather than attempting ML-based detection.
type ContentFilter struct {
	maxLength int
}

// NewContentFilter creates a ContentFilter. maxLength=0 uses 32000 (token-safe default).
func NewContentFilter(maxLength int) *ContentFilter {
	if maxLength <= 0 {
		maxLength = 32000
	}
	return &ContentFilter{maxLength: maxLength}
}

// Check returns a FilterResult for the given user message.
func (f *ContentFilter) Check(text string) FilterResult {
	if len(text) > f.maxLength {
		return FilterResult{
			Blocked: true,
			Reason:  "message exceeds maximum allowed length",
		}
	}

	lower := strings.ToLower(normalizeWhitespace(text))

	for _, pattern := range injectionPatterns {
		if strings.Contains(lower, pattern) {
			return FilterResult{
				Blocked: true,
				Reason:  "message contains disallowed instruction pattern",
			}
		}
	}

	return FilterResult{Blocked: false}
}

// injectionPatterns are lowercased substrings that commonly appear in prompt
// injection attempts. This list is kept short and high-precision to avoid
// false positives on legitimate user messages.
var injectionPatterns = []string{
	// Role/identity hijacking
	"ignore previous instructions",
	"ignore all previous",
	"disregard previous",
	"forget your instructions",
	"forget all previous instructions",
	"you are now",
	"act as if you are",
	"pretend you are",
	"your new instructions are",
	"your real instructions",
	// System prompt extraction
	"repeat your system prompt",
	"print your system prompt",
	"reveal your system prompt",
	"show your system prompt",
	"output your system prompt",
	"what are your system instructions",
	"tell me your system instructions",
	// DAN / jailbreak patterns
	"jailbreak",
	"dan mode",
	"developer mode enabled",
	"enable developer mode",
	// Delimiter confusion
	"</system>",
	"<|system|>",
	"[system]",
	"##system",
}

// normalizeWhitespace collapses consecutive whitespace and control chars to a
// single space so multi-line injection patterns are still detected.
func normalizeWhitespace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
		} else {
			sb.WriteRune(r)
			inSpace = false
		}
	}
	return sb.String()
}
