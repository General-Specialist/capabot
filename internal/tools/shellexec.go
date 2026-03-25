package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/polymath/gostaff/internal/agent"
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
func (t *ShellExecTool) Description() string { return "Execute one or more shell commands (allowlist-only)." }

func (t *ShellExecTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"commands": {
				"type": "array",
				"description": "List of commands to run sequentially. Use this to batch multiple operations.",
				"items": {
					"type": "object",
					"properties": {
						"command": {"type": "string", "description": "Command binary name (checked against allowlist)"},
						"args":    {"type": "array", "items": {"type": "string"}, "description": "Arguments"},
						"cwd":     {"type": "string", "description": "Working directory (optional)"}
					},
					"required": ["command"]
				}
			},
			"command": {"type": "string", "description": "Single command binary name (shorthand for one-item commands array)"},
			"args":    {"type": "array", "items": {"type": "string"}, "description": "Arguments for single command"},
			"cwd":     {"type": "string", "description": "Working directory for single command (optional)"}
		}
	}`)
}

type cmdSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
}

func (t *ShellExecTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Commands []cmdSpec `json:"commands"`
		Command  string    `json:"command"`
		Args     []string  `json:"args"`
		CWD      string    `json:"cwd"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}

	// Normalise: single command shorthand → commands array
	cmds := p.Commands
	if len(cmds) == 0 {
		if p.Command == "" {
			return agent.ToolResult{Content: "command is required", IsError: true}, nil
		}
		cmds = []cmdSpec{{Command: p.Command, Args: p.Args, CWD: p.CWD}}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeoutSec)*time.Second)
	defer cancel()

	var sb strings.Builder
	anyError := false

	for i, spec := range cmds {
		if spec.Command == "" {
			continue
		}

		// Allowlist check
		if len(t.allowlist) == 0 || !t.allowlist[spec.Command] {
			if len(cmds) > 1 {
				fmt.Fprintf(&sb, "[%d] %s: not in allowlist\n", i+1, spec.Command)
			} else {
				sb.WriteString(fmt.Sprintf("command %q is not in the allowlist", spec.Command))
			}
			anyError = true
			continue
		}

		binary, err := exec.LookPath(spec.Command)
		if err != nil {
			fmt.Fprintf(&sb, "[%d] %s: not found in PATH\n", i+1, spec.Command)
			anyError = true
			continue
		}

		args := make([]string, len(spec.Args))
		copy(args, spec.Args)

		//nolint:gosec // G204: allowlist-checked binary path only
		cmd := exec.CommandContext(ctx, binary, args...)
		if spec.CWD != "" {
			cmd.Dir = spec.CWD
		}

		cmd.Stdin = nil // explicitly no stdin — prevents commands like grep from blocking
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
			anyError = true
		}

		if len(cmds) == 1 {
			fmt.Fprintf(&sb, "exit code: %d\n%s", exitCode, out.String())
		} else {
			fmt.Fprintf(&sb, "[%d] %s (exit %d)\n%s\n", i+1, spec.Command, exitCode, out.String())
		}
	}

	return agent.ToolResult{Content: sb.String(), IsError: anyError}, nil
}
