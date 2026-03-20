package llm

import (
	"context"
	"encoding/json"
)

// Provider is the interface all LLM backends must implement.
type Provider interface {
	// Chat sends a non-streaming request and returns the full response.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Stream sends a streaming request and returns a channel of chunks.
	// The channel is closed when the response is complete or an error occurs.
	Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)

	// Models returns the list of models available from this provider.
	Models() []ModelInfo

	// Name returns the provider's identifier (e.g., "anthropic", "openai").
	Name() string
}

// ChatRequest represents a request to an LLM provider.
type ChatRequest struct {
	Model       string            `json:"model"`
	Messages    []ChatMessage     `json:"messages"`
	System      string            `json:"system,omitempty"`
	Tools       []ToolDefinition  `json:"tools,omitempty"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature *float64          `json:"temperature,omitempty"`
	StopSeqs    []string          `json:"stop_sequences,omitempty"`
}

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []ToolCall        `json:"tool_calls,omitempty"`
	ToolResult *ToolResult       `json:"tool_result,omitempty"`
	// Metadata carries opaque provider-specific data that must be round-tripped
	// (e.g., Gemini thought signatures). Callers should copy this from
	// ChatResponse.Metadata when building the assistant message for follow-up.
	Metadata   any               `json:"-"`
}

// ToolDefinition describes a tool the LLM can call.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCall represents the LLM requesting a tool invocation.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult represents the result of a tool execution sent back to the LLM.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ChatResponse represents the full response from an LLM provider.
type ChatResponse struct {
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StopReason string     `json:"stop_reason"`
	Usage      Usage      `json:"usage"`
	// Metadata carries opaque provider-specific data that must be round-tripped
	// back in the next request (e.g., Gemini thought signatures).
	Metadata   any        `json:"-"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StreamChunk represents a single piece of a streaming response.
type StreamChunk struct {
	Delta    string    `json:"delta,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Done     bool      `json:"done"`
	Usage    *Usage    `json:"usage,omitempty"`
	Err      error     `json:"-"`
}

// ModelInfo describes an available model.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window"`
}
