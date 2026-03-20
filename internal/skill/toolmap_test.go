package skill_test

import (
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

func TestMapToolName_Known(t *testing.T) {
	name, ok := skill.MapToolName("exec")
	if !ok {
		t.Error("expected exec to be a known mapping")
	}
	if name != "shell_exec" {
		t.Errorf("exec mapped to %q, want %q", name, "shell_exec")
	}
}

func TestMapToolName_Unknown(t *testing.T) {
	name, ok := skill.MapToolName("totally_custom_tool")
	if ok {
		t.Error("expected unknown tool to return false")
	}
	if name != "totally_custom_tool" {
		t.Errorf("unknown tool should pass through, got %q", name)
	}
}

func TestMapToolNames_Mixed(t *testing.T) {
	mapped, unmapped := skill.MapToolNames([]string{"exec", "read", "custom_thing", "web_fetch"})

	if len(mapped) != 3 {
		t.Errorf("expected 3 mapped tools, got %d: %v", len(mapped), mapped)
	}
	if len(unmapped) != 1 || unmapped[0] != "custom_thing" {
		t.Errorf("expected [custom_thing] unmapped, got %v", unmapped)
	}
}

func TestMapToolNames_Empty(t *testing.T) {
	mapped, unmapped := skill.MapToolNames(nil)
	if mapped != nil {
		t.Errorf("expected nil mapped, got %v", mapped)
	}
	if unmapped != nil {
		t.Errorf("expected nil unmapped, got %v", unmapped)
	}
}
