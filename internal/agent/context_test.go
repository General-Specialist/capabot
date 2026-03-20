package agent

import (
	"strings"
	"testing"

	"github.com/polymath/capabot/internal/llm"
)

func TestContextManager_Budget(t *testing.T) {
	cm := NewContextManager(ContextConfig{
		ContextWindow: 100000,
		BudgetPct:     0.8,
	})
	if cm.Budget() != 80000 {
		t.Fatalf("expected budget 80000, got %d", cm.Budget())
	}
}

func TestContextManager_Defaults(t *testing.T) {
	cm := NewContextManager(ContextConfig{})
	if cm.contextWindow != 128000 {
		t.Errorf("expected default window 128000, got %d", cm.contextWindow)
	}
	if cm.budgetPct != 0.8 {
		t.Errorf("expected default budget 0.8, got %f", cm.budgetPct)
	}
	if cm.maxToolOutputTokens != 4096 {
		t.Errorf("expected default max tool output 4096, got %d", cm.maxToolOutputTokens)
	}
}

func TestContextManager_NeedsSummarization(t *testing.T) {
	cm := NewContextManager(ContextConfig{
		ContextWindow: 1000,
		BudgetPct:     0.8,
	})

	// Under budget
	cm.RecordUsage(llm.Usage{InputTokens: 500})
	if cm.NeedsSummarization() {
		t.Error("should not need summarization at 500/800")
	}

	// At budget
	cm.RecordUsage(llm.Usage{InputTokens: 800})
	if !cm.NeedsSummarization() {
		t.Error("should need summarization at 800/800")
	}

	// Over budget
	cm.RecordUsage(llm.Usage{InputTokens: 900})
	if !cm.NeedsSummarization() {
		t.Error("should need summarization at 900/800")
	}
}

func TestContextManager_TruncateToolOutput(t *testing.T) {
	cm := NewContextManager(ContextConfig{
		MaxToolOutputTokens: 10, // 10 tokens * 4 chars = 40 chars max
	})

	// Short output — no truncation
	short := "hello"
	out, truncated := cm.TruncateToolOutput(short)
	if truncated {
		t.Error("expected no truncation for short output")
	}
	if out != short {
		t.Errorf("expected %q, got %q", short, out)
	}

	// Long output — should truncate
	long := strings.Repeat("x", 100)
	out, truncated = cm.TruncateToolOutput(long)
	if !truncated {
		t.Error("expected truncation for long output")
	}
	if len(out) > 100 {
		t.Errorf("truncated output too long: %d chars", len(out))
	}
	if !strings.Contains(out, "[output truncated") {
		t.Error("expected truncation marker in output")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"hi", 1},
		{"hello world!", 3},
		{strings.Repeat("a", 100), 25},
	}
	for _, tc := range tests {
		got := EstimateTokens(tc.input)
		if got != tc.expected {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}

func TestBuildMessages_Empty(t *testing.T) {
	result := BuildMessages(nil, 10)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestBuildMessages_UnderWindow(t *testing.T) {
	msgs := []llm.ChatMessage{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	result := BuildMessages(msgs, 10)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
}

func TestBuildMessages_SlidingWindow(t *testing.T) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "msg1"},
		{Role: "assistant", Content: "resp1"},
		{Role: "user", Content: "msg2"},
		{Role: "assistant", Content: "resp2"},
		{Role: "user", Content: "msg3"},
	}

	// Window of 4: keep first + last 3
	result := BuildMessages(msgs, 4)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	if result[0].Content != "you are helpful" {
		t.Error("first message should be system prompt")
	}
	if result[1].Content != "msg2" {
		t.Errorf("expected 'msg2', got %q", result[1].Content)
	}
	if result[2].Content != "resp2" {
		t.Errorf("expected 'resp2', got %q", result[2].Content)
	}
	if result[3].Content != "msg3" {
		t.Errorf("expected 'msg3', got %q", result[3].Content)
	}
}

func TestBuildMessages_DoesNotMutateInput(t *testing.T) {
	msgs := []llm.ChatMessage{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	original := make([]llm.ChatMessage, len(msgs))
	copy(original, msgs)

	_ = BuildMessages(msgs, 10)

	for i := range msgs {
		if msgs[i].Content != original[i].Content {
			t.Error("BuildMessages mutated the input slice")
		}
	}
}
