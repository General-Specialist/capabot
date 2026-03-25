package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/polymath/gostaff/internal/agent"
)

// Shell execution modes.
const (
	ShellModeAllowlist = "allowlist" // only allowlisted commands (default)
	ShellModePrompt    = "prompt"    // ask user to approve non-allowlisted commands
	ShellModeAllowAll  = "allow_all" // no restrictions
)

// ShellApprovalStore reads and writes the persistent approved-commands list.
type ShellApprovalStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

const shellApprovedKey = "shell_approved_commands"

// PendingCommand is a command awaiting user approval, keyed by channel.
type PendingCommand struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
}

// ShellExecTool implements the shell_exec tool with configurable security modes.
type ShellExecTool struct {
	allowlist  map[string]bool
	timeoutSec int
	modeFunc   func() string      // returns current mode; called on each Execute
	store      ShellApprovalStore // for persistent "always allow" approvals; nil = no persistence

	// sessionApproved tracks commands approved for the current process lifetime.
	sessionApproved   map[string]bool
	sessionApprovedMu sync.RWMutex

	// pending tracks commands awaiting approval per channel (for stateless transports).
	pending   map[string]PendingCommand
	pendingMu sync.Mutex
}

// NewShellExecTool creates a shell_exec tool.
// allowlist is the set of permitted command names; empty means all are blocked.
// timeoutSec is the execution timeout; 0 defaults to 30 seconds.
// modeFunc returns the active shell mode ("allowlist", "prompt", "allow_all").
// If nil, defaults to "allowlist".
// store enables persistent "always allow" approvals; nil disables persistence.
func NewShellExecTool(allowlist []string, timeoutSec int, modeFunc func() string, store ShellApprovalStore) *ShellExecTool {
	al := make(map[string]bool, len(allowlist))
	for _, cmd := range allowlist {
		al[cmd] = true
	}
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if modeFunc == nil {
		modeFunc = func() string { return ShellModeAllowlist }
	}
	return &ShellExecTool{
		allowlist:       al,
		timeoutSec:      timeoutSec,
		modeFunc:        modeFunc,
		store:           store,
		sessionApproved: make(map[string]bool),
		pending:         make(map[string]PendingCommand),
	}
}

// SetPending stores a command pending approval for a channel.
// Called by the tool itself when a command is denied in prompt mode.
func (t *ShellExecTool) SetPending(channelID string, cmd PendingCommand) {
	t.pendingMu.Lock()
	t.pending[channelID] = cmd
	t.pendingMu.Unlock()
}

// TakePending removes and returns the pending command for a channel, if any.
func (t *ShellExecTool) TakePending(channelID string) (PendingCommand, bool) {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	cmd, ok := t.pending[channelID]
	if ok {
		delete(t.pending, channelID)
	}
	return cmd, ok
}

// RunCommand executes a single command directly (bypassing the LLM loop).
// Used by the message handler to execute approved pending commands.
func (t *ShellExecTool) RunCommand(ctx context.Context, spec PendingCommand) (string, error) {
	binary, err := exec.LookPath(spec.Command)
	if err != nil {
		return "", fmt.Errorf("command %q not found in PATH", spec.Command)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeoutSec)*time.Second)
	defer cancel()

	args := make([]string, len(spec.Args))
	copy(args, spec.Args)

	//nolint:gosec // G204: user-approved command
	cmd := exec.CommandContext(ctx, binary, args...)
	if spec.CWD != "" {
		cmd.Dir = spec.CWD
	}
	cmd.Stdin = nil
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
	return fmt.Sprintf("exit code: %d\n%s", exitCode, out.String()), nil
}

// ApproveSession adds a command to the session-approved set.
func (t *ShellExecTool) ApproveSession(command string) {
	t.sessionApprovedMu.Lock()
	t.sessionApproved[command] = true
	t.sessionApprovedMu.Unlock()
}

// ApprovePermanent adds a command to the persistent approved list.
func (t *ShellExecTool) ApprovePermanent(ctx context.Context, command string) {
	t.addPersistentApproval(ctx, command)
}

// Mode returns the current shell mode.
func (t *ShellExecTool) Mode() string {
	return t.modeFunc()
}

func (t *ShellExecTool) Name() string { return "shell_exec" }

func (t *ShellExecTool) Description() string {
	return "Execute one or more shell commands. Commands not in the allowlist may require user approval depending on the shell_mode setting."
}

func (t *ShellExecTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"commands": {
				"type": "array",
				"description": "List of commands to run sequentially.",
				"items": {
					"type": "object",
					"properties": {
						"command":      {"type": "string", "description": "Command binary name"},
						"args":         {"type": "array", "items": {"type": "string"}, "description": "Arguments"},
						"cwd":          {"type": "string", "description": "Working directory (optional)"},
						"approved":     {"type": "boolean", "description": "Allow this command for the current session only (prompt mode)."},
						"always_allow": {"type": "boolean", "description": "Permanently add this command to the approved list (prompt mode)."}
					},
					"required": ["command"]
				}
			},
			"command":      {"type": "string", "description": "Single command binary name"},
			"args":         {"type": "array", "items": {"type": "string"}, "description": "Arguments for single command"},
			"cwd":          {"type": "string", "description": "Working directory for single command (optional)"},
			"approved":     {"type": "boolean", "description": "Allow this command for the current session only (prompt mode)."},
			"always_allow": {"type": "boolean", "description": "Permanently add this command to the approved list (prompt mode)."}
		}
	}`)
}

type cmdSpec struct {
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	CWD         string   `json:"cwd"`
	Approved    bool     `json:"approved"`
	AlwaysAllow bool     `json:"always_allow"`
}

// sessionIDKey is a context key for the channel/session ID.
type sessionIDKey struct{}

// WithSessionID attaches a session/channel ID to the context.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}

func sessionIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(sessionIDKey{}).(string)
	return v
}

func (t *ShellExecTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Commands    []cmdSpec `json:"commands"`
		Command     string    `json:"command"`
		Args        []string  `json:"args"`
		CWD         string    `json:"cwd"`
		Approved    bool      `json:"approved"`
		AlwaysAllow bool      `json:"always_allow"`
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
		cmds = []cmdSpec{{Command: p.Command, Args: p.Args, CWD: p.CWD, Approved: p.Approved, AlwaysAllow: p.AlwaysAllow}}
	}

	mode := t.modeFunc()
	channelID := sessionIDFromCtx(ctx)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeoutSec)*time.Second)
	defer cancel()

	var sb strings.Builder
	anyError := false

	for i, spec := range cmds {
		if spec.Command == "" {
			continue
		}

		// Check if command is allowed based on mode.
		if !t.isCommandAllowed(ctx, mode, spec.Command, spec.Approved, spec.AlwaysAllow) {
			// Store as pending so stateless transports can handle the approval.
			if channelID != "" {
				t.SetPending(channelID, PendingCommand{
					Command: spec.Command,
					Args:    spec.Args,
					CWD:     spec.CWD,
				})
			}
			msg := t.buildDenialMessage(mode, spec, i, len(cmds))
			sb.WriteString(msg)
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

// isCommandAllowed checks whether a command is permitted to run.
func (t *ShellExecTool) isCommandAllowed(ctx context.Context, mode, command string, approved, alwaysAllow bool) bool {
	switch mode {
	case ShellModeAllowAll:
		return true

	case ShellModePrompt:
		// Static allowlist.
		if t.allowlist[command] {
			return true
		}
		// Persistently approved.
		if t.isPersistentlyApproved(ctx, command) {
			return true
		}
		// Session approved.
		t.sessionApprovedMu.RLock()
		sessionOK := t.sessionApproved[command]
		t.sessionApprovedMu.RUnlock()
		if sessionOK {
			return true
		}
		// User just approved permanently.
		if alwaysAllow {
			t.addPersistentApproval(ctx, command)
			return true
		}
		// User just approved for session.
		if approved {
			t.sessionApprovedMu.Lock()
			t.sessionApproved[command] = true
			t.sessionApprovedMu.Unlock()
			return true
		}
		return false

	default: // ShellModeAllowlist
		return len(t.allowlist) > 0 && t.allowlist[command]
	}
}

// isPersistentlyApproved checks the settings DB for a permanently approved command.
func (t *ShellExecTool) isPersistentlyApproved(ctx context.Context, command string) bool {
	if t.store == nil {
		return false
	}
	raw, err := t.store.GetSetting(ctx, shellApprovedKey)
	if err != nil || raw == "" {
		return false
	}
	var cmds []string
	if json.Unmarshal([]byte(raw), &cmds) != nil {
		return false
	}
	for _, c := range cmds {
		if c == command {
			return true
		}
	}
	return false
}

// addPersistentApproval adds a command to the permanently approved list in the settings DB.
func (t *ShellExecTool) addPersistentApproval(ctx context.Context, command string) {
	if t.store == nil {
		// Fall back to session approval when no store is available.
		t.sessionApprovedMu.Lock()
		t.sessionApproved[command] = true
		t.sessionApprovedMu.Unlock()
		return
	}
	raw, _ := t.store.GetSetting(ctx, shellApprovedKey)
	var cmds []string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cmds)
	}
	for _, c := range cmds {
		if c == command {
			return // already present
		}
	}
	cmds = append(cmds, command)
	data, _ := json.Marshal(cmds)
	_ = t.store.SetSetting(ctx, shellApprovedKey, string(data))
}

// buildDenialMessage returns the appropriate error message for a denied command.
func (t *ShellExecTool) buildDenialMessage(mode string, spec cmdSpec, index, total int) string {
	if mode == ShellModePrompt {
		argsStr := ""
		if len(spec.Args) > 0 {
			argsStr = " " + strings.Join(spec.Args, " ")
		}
		msg := fmt.Sprintf(
			"Command %q%s is not in the allowlist. Ask the user if they want to allow it. "+
				"If they say yes for this session, retry with approved: true. "+
				"If they say yes permanently, retry with always_allow: true.",
			spec.Command, argsStr,
		)
		if total > 1 {
			return fmt.Sprintf("[%d] %s\n", index+1, msg)
		}
		return msg
	}

	// Default allowlist mode.
	if total > 1 {
		return fmt.Sprintf("[%d] %s: not in allowlist\n", index+1, spec.Command)
	}
	return fmt.Sprintf("command %q is not in the allowlist", spec.Command)
}
