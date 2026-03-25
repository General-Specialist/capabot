package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/polymath/capabot/internal/llm"
)

// Tool is the interface all agent tools must implement.
type Tool interface {
	// Name returns the unique tool identifier (e.g., "web_search", "shell_exec").
	Name() string

	// Description returns a human-readable description for the LLM.
	Description() string

	// Parameters returns the JSON Schema describing the tool's input.
	Parameters() json.RawMessage

	// Execute runs the tool with the given parameters and returns the result.
	Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	Content string          `json:"content"`
	IsError bool            `json:"is_error,omitempty"`
	Parts   []llm.MediaPart `json:"-"` // optional multimodal attachments (images, PDFs)
}

// Registry holds available tools, keyed by name.
// Tools can be marked as "extended" — these are not sent to the LLM directly
// but are accessible via the use_tool meta-tool.
type Registry struct {
	mu       sync.RWMutex
	tools    map[string]Tool
	extended map[string]bool // true = extended (lazy-loaded via use_tool)
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:    make(map[string]Tool),
		extended: make(map[string]bool),
	}
}

// Register adds a core tool to the registry.
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	r.tools[name] = tool
	return nil
}

// RegisterExtended adds an extended tool (not sent to LLM, accessed via use_tool).
func (r *Registry) RegisterExtended(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	r.tools[name] = tool
	r.extended[name] = true
	return nil
}

// Get retrieves a tool by name (both core and extended). Returns nil if not found.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List returns core tools only (sent to LLM as definitions).
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Tool, 0, len(r.tools))
	for name, t := range r.tools {
		if !r.extended[name] {
			out = append(out, t)
		}
	}
	return out
}

// ExtendedNames returns sorted names of extended tools (for use_tool description).
func (r *Registry) ExtendedNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.extended))
	for name := range r.extended {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ExtendedDescriptions returns "name: description" for each extended tool.
func (r *Registry) ExtendedDescriptions() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.extended))
	for name := range r.extended {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			fmt.Fprintf(&sb, "- %s: %s\n", name, t.Description())
		}
	}
	return sb.String()
}

// Names returns sorted names of all tools (core + extended).
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Len returns the total number of registered tools (core + extended).
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

