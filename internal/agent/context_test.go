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

func TestBuildMessages_SkipsOrphanedToolResults(t *testing.T) {
	// Window cuts right before a tool result — the tool result is orphaned
	// (its parent assistant+tool_calls was cut). BuildMessages must advance
	// the boundary forward so the LLM never sees an orphaned tool message.
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "t1"}}},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "t1"}},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "follow-up"},
	}
	// windowSize=4 → tail starts at index 2 (assistant+tool_calls).
	// That's fine, but windowSize=3 → tail starts at index 3 (tool) — orphaned.
	result := BuildMessages(msgs, 3)
	for _, m := range result[1:] { // skip the always-kept first message
		if m.Role == "tool" {
			t.Error("BuildMessages returned an orphaned tool message")
		}
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

func TestCompressOldToolOutputs(t *testing.T) {
	longOutput := strings.Repeat("line of output\n", 50) // ~750 chars

	history := []llm.ChatMessage{
		{Role: "user", Content: "do something"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "shell_exec"}}},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "t1", Content: longOutput}},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "t2", Name: "file_read"}}},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "t2", Content: longOutput}},
	}

	// No summarization model — uses dumb truncation
	a := newTestAgent(&mockProvider{}, NewRegistry())
	a.compressOldToolOutputs(t.Context(), history)

	// t1 (old) should be compressed
	if len(history[2].ToolResult.Content) >= len(longOutput) {
		t.Error("expected old tool output to be compressed")
	}
	if !strings.Contains(history[2].ToolResult.Content, "[condensed:") {
		t.Error("expected condensed marker in old output")
	}

	// t2 (current) should be unchanged
	if history[4].ToolResult.Content != longOutput {
		t.Error("expected current tool output to remain full")
	}
}

func TestCompressOldToolOutputs_ShortOutputsUntouched(t *testing.T) {
	history := []llm.ChatMessage{
		{Role: "user", Content: "do something"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "file_edit"}}},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "t1", Content: "edited /path/to/file"}},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "t2", Name: "file_read"}}},
		{Role: "tool", ToolResult: &llm.ToolResult{ToolUseID: "t2", Content: "some content"}},
	}

	a := newTestAgent(&mockProvider{}, NewRegistry())
	a.compressOldToolOutputs(t.Context(), history)

	if history[2].ToolResult.Content != "edited /path/to/file" {
		t.Error("short tool output should not be compressed")
	}
}
