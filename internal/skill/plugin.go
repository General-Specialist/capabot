package skill

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed shim/host.mjs shim/openclaw-sdk/plugin-entry.mjs shim/openclaw-sdk/core.mjs
var shimFS embed.FS

// pluginEntryPoints is the probe order for detecting a plugin's entry point.
var pluginEntryPoints = []string{"index.ts", "index.js", "index.py"}

// pluginRuntimes maps entry-point extensions to their runtime binary.
var pluginRuntimes = map[string]string{
	"index.ts": "bun",
	"index.js": "node",
	"index.py": "python3",
}

// RegisteredTool describes a tool registered by a plugin during init.
type RegisteredTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// RegisteredHook describes a hook registered by a plugin during init.
type RegisteredHook struct {
	Event string `json:"event"` // "pre_tool_use" or "post_tool_use"
	Name  string `json:"name"`
}

// RegisteredRoute describes an HTTP route registered by a plugin during init.
type RegisteredRoute struct {
	Method string `json:"method"` // GET, POST, etc.
	Path   string `json:"path"`   // e.g. "/api/plugins/my-plugin/status"
}

// RegisteredProvider describes an LLM provider registered by a plugin during init.
type RegisteredProvider struct {
	Name   string          `json:"name"`
	Models json.RawMessage `json:"models"`
}

// pluginMsg is a JSON-line message in the host↔plugin protocol.
type pluginMsg struct {
	Type string `json:"type"`

	// Shared fields
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	ID          string          `json:"id,omitempty"`

	// register_hook / hook
	Event string `json:"event,omitempty"`

	// register_http_route / http
	Method  string          `json:"method,omitempty"`
	Path    string          `json:"path,omitempty"`
	Headers json.RawMessage `json:"headers,omitempty"`
	Query   json.RawMessage `json:"query,omitempty"`
	Body    string          `json:"body,omitempty"`

	// register_provider / chat
	Models   json.RawMessage `json:"models,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Model    string          `json:"model,omitempty"`
	Messages json.RawMessage `json:"messages,omitempty"`
	System   string          `json:"system,omitempty"`
	Tools    json.RawMessage `json:"tools,omitempty"`
	MaxTok   int             `json:"max_tokens,omitempty"`

	// invoke
	Tool   string          `json:"tool,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`

	// result / hook_result
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Allow     *bool           `json:"allow,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
	Usage     json.RawMessage `json:"usage,omitempty"`

	// http_response
	Status int `json:"status,omitempty"`

	// error
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// PluginProcess manages a long-running plugin subprocess.
// During init, the plugin registers tools, hooks, routes, and providers.
// After "ready", the host dispatches invocations via JSON-line messages.
type PluginProcess struct {
	dir        string
	entryPoint string
	runtime    string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner

	tools     []RegisteredTool
	hooks     []RegisteredHook
	routes    []RegisteredRoute
	providers []RegisteredProvider

	mu      sync.Mutex
	pending map[string]chan pluginMsg
	nextID  atomic.Int64
}

// NewPluginProcess detects the entry point in skillDir, writes the embedded
// shim to a temp file, spawns the subprocess, and waits for registrations.
// Returns after the plugin sends "ready" or a timeout of 10s.
func NewPluginProcess(ctx context.Context, skillDir string) (*PluginProcess, error) {
	var entryPoint, runtime string
	for _, ep := range pluginEntryPoints {
		if _, err := os.Stat(filepath.Join(skillDir, ep)); err == nil {
			rt, ok := pluginRuntimes[ep]
			if !ok {
				continue
			}
			if _, err := exec.LookPath(rt); err != nil {
				return nil, fmt.Errorf("runtime %q required by %s not found on PATH", rt, ep)
			}
			entryPoint = ep
			runtime = rt
			break
		}
	}
	if entryPoint == "" {
		return nil, fmt.Errorf("no supported entry point found in %s", skillDir)
	}

	shimData, err := shimFS.ReadFile("shim/host.mjs")
	if err != nil {
		return nil, fmt.Errorf("reading embedded shim: %w", err)
	}
	shimPath := filepath.Join(os.TempDir(), "capabot-plugin-shim.mjs")
	if err := os.WriteFile(shimPath, shimData, 0o644); err != nil {
		return nil, fmt.Errorf("writing shim: %w", err)
	}

	// Write OpenClaw SDK compatibility shim so plugins can
	// import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry"
	if err := writeOpenClawSDKShim(skillDir); err != nil {
		return nil, fmt.Errorf("writing openclaw SDK shim: %w", err)
	}

	args := []string{shimPath, "./" + entryPoint}
	if runtime == "bun" {
		args = append([]string{"run"}, args...)
	}

	cmd := exec.CommandContext(ctx, runtime, args...)
	cmd.Dir = skillDir
	cmd.Stderr = os.Stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	p := &PluginProcess{
		dir:        skillDir,
		entryPoint: entryPoint,
		runtime:    runtime,
		cmd:        cmd,
		stdin:      stdinPipe,
		stdout:     bufio.NewScanner(stdoutPipe),
		pending:    make(map[string]chan pluginMsg),
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting plugin: %w", err)
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := p.readRegistrations(initCtx); err != nil {
		cmd.Process.Kill() //nolint:errcheck
		return nil, err
	}

	go p.readLoop()

	return p, nil
}

// readRegistrations reads messages from the plugin until "ready" or context timeout.
func (p *PluginProcess) readRegistrations(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		for p.stdout.Scan() {
			var msg pluginMsg
			if err := json.Unmarshal(p.stdout.Bytes(), &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "register_tool":
				p.tools = append(p.tools, RegisteredTool{
					Name:        msg.Name,
					Description: msg.Description,
					Parameters:  msg.Parameters,
				})
			case "register_hook":
				p.hooks = append(p.hooks, RegisteredHook{
					Event: msg.Event,
					Name:  msg.Name,
				})
			case "register_http_route":
				p.routes = append(p.routes, RegisteredRoute{
					Method: msg.Method,
					Path:   msg.Path,
				})
			case "register_provider":
				p.providers = append(p.providers, RegisteredProvider{
					Name:   msg.Name,
					Models: msg.Models,
				})
			case "ready":
				done <- nil
				return
			case "error":
				done <- fmt.Errorf("plugin init error: %s", msg.Message)
				return
			}
		}
		if err := p.stdout.Err(); err != nil {
			done <- fmt.Errorf("reading plugin stdout: %w", err)
		} else {
			done <- fmt.Errorf("plugin exited before sending ready")
		}
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("plugin init timed out after 10s")
	}
}

// readLoop reads JSON-line responses from the plugin and dispatches them
// to the appropriate pending channel by ID.
func (p *PluginProcess) readLoop() {
	for p.stdout.Scan() {
		var msg pluginMsg
		if err := json.Unmarshal(p.stdout.Bytes(), &msg); err != nil {
			continue
		}
		if msg.ID != "" {
			p.mu.Lock()
			ch, ok := p.pending[msg.ID]
			if ok {
				delete(p.pending, msg.ID)
			}
			p.mu.Unlock()
			if ok {
				ch <- msg
			}
		}
	}
	p.mu.Lock()
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.mu.Unlock()
}

// sendAndWait sends a message and waits for the response with the matching ID.
func (p *PluginProcess) sendAndWait(ctx context.Context, msg pluginMsg) (pluginMsg, error) {
	id := fmt.Sprintf("%d", p.nextID.Add(1))
	msg.ID = id

	ch := make(chan pluginMsg, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return pluginMsg{}, fmt.Errorf("marshaling message: %w", err)
	}
	data = append(data, '\n')

	if _, err := p.stdin.Write(data); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return pluginMsg{}, fmt.Errorf("writing to plugin stdin: %w", err)
	}

	select {
	case result, ok := <-ch:
		if !ok {
			return pluginMsg{}, fmt.Errorf("plugin process exited")
		}
		return result, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return pluginMsg{}, ctx.Err()
	}
}

// --- Accessors ---

func (p *PluginProcess) Tools() []RegisteredTool         { return p.tools }
func (p *PluginProcess) Hooks() []RegisteredHook          { return p.hooks }
func (p *PluginProcess) Routes() []RegisteredRoute        { return p.routes }
func (p *PluginProcess) Providers() []RegisteredProvider   { return p.providers }
func (p *PluginProcess) EntryPoint() string                { return p.entryPoint }
func (p *PluginProcess) Runtime() string                   { return p.runtime }

// --- Invocation methods ---

// Invoke calls a registered tool by name.
func (p *PluginProcess) Invoke(ctx context.Context, toolName string, params json.RawMessage) (SkillResult, error) {
	resp, err := p.sendAndWait(ctx, pluginMsg{
		Type:   "invoke",
		Tool:   toolName,
		Params: params,
	})
	if err != nil {
		return SkillResult{}, err
	}
	return SkillResult{Content: resp.Content, IsError: resp.IsError}, nil
}

// HookResult is the outcome of running a plugin hook.
type HookResult struct {
	Allow  bool            // false = block the tool execution
	Params json.RawMessage // optionally modified params (pre_tool_use)
	Result json.RawMessage // optionally modified result (post_tool_use)
}

// InvokeHook sends a hook event to the plugin and returns the result.
func (p *PluginProcess) InvokeHook(ctx context.Context, event, toolName string, params json.RawMessage, result json.RawMessage) (HookResult, error) {
	resp, err := p.sendAndWait(ctx, pluginMsg{
		Type:   "hook",
		Event:  event,
		Tool:   toolName,
		Params: params,
		Result: result,
	})
	if err != nil {
		return HookResult{Allow: true}, err
	}

	allow := true
	if resp.Allow != nil {
		allow = *resp.Allow
	}
	return HookResult{
		Allow:  allow,
		Params: resp.Params,
		Result: resp.Result,
	}, nil
}

// HTTPRequest describes an HTTP request to forward to a plugin route handler.
type HTTPRequest struct {
	Method  string
	Path    string
	Headers map[string]string
	Query   map[string]string
	Body    string
}

// HTTPResponse is the plugin's response to an HTTP request.
type HTTPResponse struct {
	Status  int
	Headers map[string]string
	Body    string
}

// InvokeHTTP forwards an HTTP request to the plugin's route handler.
func (p *PluginProcess) InvokeHTTP(ctx context.Context, req HTTPRequest) (HTTPResponse, error) {
	headersJSON, _ := json.Marshal(req.Headers)
	queryJSON, _ := json.Marshal(req.Query)

	resp, err := p.sendAndWait(ctx, pluginMsg{
		Type:    "http",
		Method:  req.Method,
		Path:    req.Path,
		Headers: headersJSON,
		Query:   queryJSON,
		Body:    req.Body,
	})
	if err != nil {
		return HTTPResponse{Status: 500, Body: err.Error()}, err
	}

	status := resp.Status
	if status == 0 {
		status = 200
	}
	return HTTPResponse{
		Status: status,
		Body:   resp.Body,
	}, nil
}

// ChatRequest describes a chat request to forward to a plugin provider.
type ChatRequest struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	System   string          `json:"system"`
	Tools    json.RawMessage `json:"tools"`
	MaxTok   int             `json:"max_tokens"`
}

// ChatResponse is the plugin provider's response.
type ChatResponse struct {
	Content   string          `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
	Usage     json.RawMessage `json:"usage"`
	Model     string          `json:"model"`
	Error     string          `json:"error"`
}

// InvokeChat forwards a chat request to the plugin's LLM provider.
func (p *PluginProcess) InvokeChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	resp, err := p.sendAndWait(ctx, pluginMsg{
		Type:     "chat",
		Provider: req.Provider,
		Model:    req.Model,
		Messages: req.Messages,
		System:   req.System,
		Tools:    req.Tools,
		MaxTok:   req.MaxTok,
	})
	if err != nil {
		return ChatResponse{Error: err.Error()}, err
	}
	return ChatResponse{
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
		Usage:     resp.Usage,
		Model:     resp.Model,
		Error:     resp.Error,
	}, nil
}

// Close sends a shutdown message and waits for the process to exit.
func (p *PluginProcess) Close() error {
	msg, _ := json.Marshal(pluginMsg{Type: "shutdown"})
	msg = append(msg, '\n')
	p.stdin.Write(msg) //nolint:errcheck
	p.stdin.Close()    //nolint:errcheck
	return p.cmd.Wait()
}

// writeOpenClawSDKShim writes the embedded OpenClaw plugin-sdk compatibility
// shim into the plugin's node_modules so that
//   import { definePluginEntry } from "openclaw/plugin-sdk/plugin-entry"
// resolves correctly.
func writeOpenClawSDKShim(skillDir string) error {
	sdkDir := filepath.Join(skillDir, "node_modules", "openclaw", "plugin-sdk")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		return err
	}

	for _, name := range []string{"plugin-entry.mjs", "core.mjs"} {
		data, err := shimFS.ReadFile("shim/openclaw-sdk/" + name)
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(sdkDir, name), data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}

	// Write package.json so node/bun resolve the .mjs exports correctly
	pkgJSON := `{"name":"openclaw","exports":{"./*":"./*","./plugin-sdk/plugin-entry":"./plugin-sdk/plugin-entry.mjs","./plugin-sdk/core":"./plugin-sdk/core.mjs"}}`
	pkgDir := filepath.Join(skillDir, "node_modules", "openclaw")
	return os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkgJSON), 0o644)
}
