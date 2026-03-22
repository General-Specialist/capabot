package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/polymath/capabot/internal/agent"
)

// ShellExecTool implements the shell_exec tool with allowlist-based security.
type ShellExecTool struct {
	allowlist  map[string]bool
	timeoutSec int
}

// NewShellExecTool creates a shell_exec tool.
// allowlist is the set of permitted command names; empty means all are blocked.
// timeoutSec is the execution timeout; 0 defaults to 30 seconds.
func NewShellExecTool(allowlist []string, timeoutSec int) *ShellExecTool {
	al := make(map[string]bool, len(allowlist))
	for _, cmd := range allowlist {
		al[cmd] = true
	}
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	return &ShellExecTool{
		allowlist:  al,
		timeoutSec: timeoutSec,
	}
}

func (t *ShellExecTool) Name() string        { return "shell_exec" }
func (t *ShellExecTool) Description() string { return "Execute a shell command (allowlist-only)." }

func (t *ShellExecTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "Command binary name (checked against allowlist)"},
			"args": {"type": "array", "items": {"type": "string"}, "description": "Arguments"},
			"cwd": {"type": "string", "description": "Working directory (optional)"}
		},
		"required": ["command"]
	}`)
}

func (t *ShellExecTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		CWD     string   `json:"cwd"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Command == "" {
		return agent.ToolResult{Content: "command is required", IsError: true}, nil
	}

	// Allowlist check — empty allowlist means everything is blocked
	if len(t.allowlist) == 0 || !t.allowlist[p.Command] {
		return agent.ToolResult{
			Content: fmt.Sprintf("command %q is not in the allowlist", p.Command),
			IsError: true,
		}, nil
	}

	// Locate the binary in PATH to avoid shell injection via PATH manipulation
	binary, err := exec.LookPath(p.Command)
	if err != nil {
		return agent.ToolResult{
			Content: fmt.Sprintf("command %q not found in PATH", p.Command),
			IsError: true,
		}, nil
	}

	// Apply timeout
	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeoutSec)*time.Second)
	defer cancel()

	// Build args immutably
	args := make([]string, len(p.Args))
	copy(args, p.Args)

	//nolint:gosec // G204: allowlist-checked binary path only
	cmd := exec.CommandContext(ctx, binary, args...)
	if p.CWD != "" {
		cmd.Dir = p.CWD
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	runErr := cmd.Run()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	content := fmt.Sprintf("exit code: %d\n%s", exitCode, out.String())
	return agent.ToolResult{Content: content, IsError: exitCode != 0}, nil
}
