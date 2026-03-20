package agent

import (
	"github.com/polymath/capabot/internal/llm"
)

// ContextManager tracks token usage across an agent loop and decides
// when messages need summarization or tool outputs need truncation.
type ContextManager struct {
	contextWindow      int     // model's total context window (tokens)
	budgetPct          float64 // fraction of window we're allowed to use (e.g., 0.8)
	maxToolOutputTokens int    // max tokens per tool output before truncation
	totalInputTokens   int     // cumulative input tokens reported by LLM
	totalOutputTokens  int     // cumulative output tokens reported by LLM
}

// ContextConfig configures the context window manager.
type ContextConfig struct {
	ContextWindow       int     // model's context window size in tokens
	BudgetPct           float64 // fraction of window to use (0.0, 1.0]
	MaxToolOutputTokens int     // max tokens per tool output
}

// NewContextManager creates a context manager with the given config.
func NewContextManager(cfg ContextConfig) *ContextManager {
	if cfg.BudgetPct <= 0 || cfg.BudgetPct > 1.0 {
		cfg.BudgetPct = 0.8
	}
	if cfg.MaxToolOutputTokens <= 0 {
		cfg.MaxToolOutputTokens = 4096
	}
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = 128000
	}
	return &ContextManager{
		contextWindow:       cfg.ContextWindow,
		budgetPct:           cfg.BudgetPct,
		maxToolOutputTokens: cfg.MaxToolOutputTokens,
	}
}

// Budget returns the maximum number of tokens we should use.
func (cm *ContextManager) Budget() int {
	return int(float64(cm.contextWindow) * cm.budgetPct)
}

// RecordUsage updates cumulative token counts from an LLM response.
func (cm *ContextManager) RecordUsage(usage llm.Usage) {
	cm.totalInputTokens = usage.InputTokens // latest prompt size
	cm.totalOutputTokens += usage.OutputTokens
}

// NeedsSummarization returns true when cumulative input tokens exceed
// the budget threshold, indicating older messages should be summarized.
func (cm *ContextManager) NeedsSummarization() bool {
	return cm.totalInputTokens >= cm.Budget()
}

// TruncateToolOutput truncates a tool output string if it exceeds
// maxToolOutputTokens (approximated as 4 chars per token).
// Returns the (possibly truncated) output and whether truncation occurred.
func (cm *ContextManager) TruncateToolOutput(output string) (string, bool) {
	maxChars := cm.maxToolOutputTokens * 4
	if len(output) <= maxChars {
		return output, false
	}
	return output[:maxChars] + "\n\n[output truncated — full content stored in memory]", true
}

// EstimateTokens gives a rough token count for a string (4 chars per token).
func EstimateTokens(s string) int {
	n := len(s) / 4
	if n == 0 && len(s) > 0 {
		return 1
	}
	return n
}

// BuildMessages converts the conversation history into LLM ChatMessages,
// applying the sliding window strategy: when there are more than
// windowSize messages, only the system message (first) and the most
// recent windowSize-1 messages are kept.
func BuildMessages(history []llm.ChatMessage, windowSize int) []llm.ChatMessage {
	if len(history) == 0 {
		return nil
	}
	if windowSize <= 0 || len(history) <= windowSize {
		result := make([]llm.ChatMessage, len(history))
		copy(result, history)
		return result
	}

	// Keep the first message (system/initial user) + tail
	result := make([]llm.ChatMessage, 0, windowSize)
	result = append(result, history[0])
	tail := history[len(history)-(windowSize-1):]
	result = append(result, tail...)
	return result
}
