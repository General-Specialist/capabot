// Package sdk provides the Go plugin SDK for GoStaff.
//
// Plugins implement the Plugin interface and register tools, hooks, routes,
// and providers via the Registrar passed to Init. Everything runs in-process
// as direct function calls — no subprocess, no serialization.
package sdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/llm"
)

// Plugin is the interface that Go plugins implement.
type Plugin interface {
	// Init is called once at startup. Register tools, hooks, routes,
	// and providers on the Registrar.
	Init(r Registrar) error

	// Close is called on graceful shutdown.
	Close() error
}

// Registrar collects registrations during Plugin.Init.
type Registrar interface {
	RegisterTool(tool agent.Tool)
	RegisterHook(hook agent.ToolHook)
	RegisterRoute(method, path string, handler http.HandlerFunc)
	RegisterProvider(name string, provider llm.Provider)
	RegisterChannel(cfg ChannelConfig)
}

// ChannelConfig describes a per-channel configuration declared by a plugin.
type ChannelConfig struct {
	ID             string
	Tag            string
	SystemPrompt   string
	SkillNames     []string
	Model          string
	MemoryIsolated bool
}

// Route is an HTTP route registered by a plugin.
type Route struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

// ProviderEntry is an LLM provider registered by a plugin.
type ProviderEntry struct {
	Name     string
	Provider llm.Provider
}

// MemoryPromptBuilder is called before each agent run to produce dynamic
// context that gets appended to the system prompt.
type MemoryPromptBuilder interface {
	// Build returns text to inject into the system prompt for this session.
	Build(ctx context.Context, sessionID string) (string, error)
	// Name returns the section name for logging/debugging.
	Name() string
}

// Registration implements Registrar and collects everything into slices.
type Registration struct {
	Tools                []agent.Tool
	Hooks                []agent.ToolHook
	Routes               []Route
	Providers            []ProviderEntry
	Channels             []ChannelConfig
	MemoryPromptBuilders []MemoryPromptBuilder
}

func (r *Registration) RegisterTool(tool agent.Tool) {
	r.Tools = append(r.Tools, tool)
}

func (r *Registration) RegisterHook(hook agent.ToolHook) {
	r.Hooks = append(r.Hooks, hook)
}

func (r *Registration) RegisterRoute(method, path string, handler http.HandlerFunc) {
	r.Routes = append(r.Routes, Route{Method: method, Path: path, Handler: handler})
}

func (r *Registration) RegisterProvider(name string, provider llm.Provider) {
	r.Providers = append(r.Providers, ProviderEntry{Name: name, Provider: provider})
}

func (r *Registration) RegisterChannel(cfg ChannelConfig) {
	r.Channels = append(r.Channels, cfg)
}

// InitPlugin initializes a plugin and returns its registrations.
func InitPlugin(p Plugin) (*Registration, error) {
	reg := &Registration{}
	if err := p.Init(reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// SimpleTool wraps a function as an agent.Tool.
type SimpleTool struct {
	ToolName   string
	ToolDesc   string
	ToolSchema json.RawMessage
	Fn         func(ctx context.Context, params json.RawMessage) (string, error)
}

func (t *SimpleTool) Name() string               { return t.ToolName }
func (t *SimpleTool) Description() string         { return t.ToolDesc }
func (t *SimpleTool) Parameters() json.RawMessage { return t.ToolSchema }

func (t *SimpleTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	content, err := t.Fn(ctx, params)
	if err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return agent.ToolResult{Content: content}, nil
}
