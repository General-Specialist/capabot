package log

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
)

func TestNew_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.InfoLevel).With().Timestamp().Logger()
	logger.Info().Msg("test message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected valid JSON output, got: %s", buf.String())
	}
	if entry["message"] != "test message" {
		t.Errorf("expected message 'test message', got %v", entry["message"])
	}
}

func TestNew_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.WarnLevel)
	logger.Info().Msg("should not appear")

	if buf.Len() != 0 {
		t.Errorf("expected no output for info at warn level, got: %s", buf.String())
	}

	logger.Warn().Msg("should appear")
	if buf.Len() == 0 {
		t.Error("expected output for warn at warn level")
	}
}

func TestWithContext_AddsFields(t *testing.T) {
	var buf bytes.Buffer
	base := zerolog.New(&buf)
	logger := WithContext(base, "tenant-1", "session-abc", "agent-x")
	logger.Info().Msg("contextual")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected valid JSON: %s", buf.String())
	}
	if entry["tenant_id"] != "tenant-1" {
		t.Errorf("expected tenant_id tenant-1, got %v", entry["tenant_id"])
	}
	if entry["session_id"] != "session-abc" {
		t.Errorf("expected session_id session-abc, got %v", entry["session_id"])
	}
	if entry["agent_id"] != "agent-x" {
		t.Errorf("expected agent_id agent-x, got %v", entry["agent_id"])
	}
}

func TestWithContext_OmitsEmpty(t *testing.T) {
	var buf bytes.Buffer
	base := zerolog.New(&buf)
	logger := WithContext(base, "tenant-1", "", "")
	logger.Info().Msg("partial")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected valid JSON: %s", buf.String())
	}
	if entry["tenant_id"] != "tenant-1" {
		t.Errorf("expected tenant_id tenant-1, got %v", entry["tenant_id"])
	}
	if _, ok := entry["session_id"]; ok {
		t.Error("expected session_id to be omitted when empty")
	}
	if _, ok := entry["agent_id"]; ok {
		t.Error("expected agent_id to be omitted when empty")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  zerolog.Level
	}{
		{"trace", zerolog.TraceLevel},
		{"debug", zerolog.DebugLevel},
		{"info", zerolog.InfoLevel},
		{"warn", zerolog.WarnLevel},
		{"error", zerolog.ErrorLevel},
		{"fatal", zerolog.FatalLevel},
		{"unknown", zerolog.InfoLevel},
		{"", zerolog.InfoLevel},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLevel(tt.input)
			if got != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
