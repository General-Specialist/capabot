package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/polymath/capabot/internal/llm"
	"github.com/rs/zerolog"
)

// EventKind identifies the type of an agent event.
type EventKind string

const (
	EventThinking  EventKind = "thinking"   // LLM is being called
	EventToolStart EventKind = "tool_start" // tool execution beginning
	EventToolEnd   EventKind = "tool_end"   // tool execution complete
	EventResponse  EventKind = "response"   // final text response
)

// AgentEvent is emitted during agent execution so callers can stream progress.
type AgentEvent struct {
	Kind      EventKind       `json:"kind"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolID    string          `json:"tool_id,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Content   string          `json:"content,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	// Iteration is the ReAct loop index (1-based).
	Iteration int `json:"iteration,omitempty"`
}

// AgentConfig holds configuration for an Agent instance.
type AgentConfig struct {
	ID            string
	Model         string
	SystemPrompt  string
	MaxIterations int
	MaxTokens     int
	Temperature   *float64
}

// StoreWriter is the subset of memory.Store the agent needs for audit logging.
type StoreWriter interface {
	SaveMessage(ctx context.Context, msg StoreMessage) (int64, error)
	SaveToolExecution(ctx context.Context, exec StoreToolExecution) error
}

// StoreMessage mirrors memory.Message fields the agent writes.
type StoreMessage struct {
	SessionID  string
	Role       string
	Content    string
	ToolCallID string
	ToolName   string
	ToolInput  string
	TokenCount int
}

// StoreToolExecution mirrors memory.ToolExecution fields the agent writes.
type StoreToolExecution struct {
	SessionID  string
	ToolName   string
	Input      string
	Output     string
	DurationMs int64
	Success    bool
}

// RunResult is the final outcome of an agent run.
type RunResult struct {
	Response   string         `json:"response"`
	ToolCalls  int            `json:"tool_calls"`
	Iterations int            `json:"iterations"`
	Usage      llm.Usage      `json:"usage"`
	StopReason string         `json:"stop_reason"`
	History    []llm.ChatMessage `json:"-"`
}

// Agent implements the ReAct loop: Observe -> Think -> Act -> Observe.
type Agent struct {
	config   AgentConfig
	provider llm.Provider
	tools    *Registry
	ctxMgr   *ContextManager
	store    StoreWriter // nil = no persistence
	logger   zerolog.Logger
	onEvent  func(AgentEvent) // nil = no streaming
}

// New creates a new Agent with the given dependencies.
func New(cfg AgentConfig, provider llm.Provider, tools *Registry, ctxMgr *ContextManager, logger zerolog.Logger) *Agent {
	if cfg.MaxIterations < 0 {
		cfg.MaxIterations = 0 // 0 = unlimited
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	return &Agent{
		config:   cfg,
		provider: provider,
		tools:    tools,
		ctxMgr:   ctxMgr,
		logger:   logger,
	}
}

// SetOnEvent registers a callback that will be called for each agent event
// during Run(). The callback must not block. Use a buffered channel or
// goroutine inside the callback if you need to fan-out.
func (a *Agent) SetOnEvent(fn func(AgentEvent)) {
	a.onEvent = fn
}

// emit sends an event if a callback is registered.
func (a *Agent) emit(e AgentEvent) {
	if a.onEvent != nil {
		a.onEvent(e)
	}
}

// SetStore attaches a persistence layer for message and tool execution logging.
func (a *Agent) SetStore(store StoreWriter) {
	a.store = store
}

// Run executes the ReAct loop for the given session and input messages.
// It loops until:
//   - The LLM responds with text only (no tool calls) -> returns response
//   - MaxIterations is reached -> returns what we have
//   - Context is cancelled -> returns error
func (a *Agent) Run(ctx context.Context, sessionID string, messages []llm.ChatMessage) (*RunResult, error) {
	history := make([]llm.ChatMessage, len(messages))
	copy(history, messages)

	result := &RunResult{}
	toolDefs := a.buildToolDefs()

	for iteration := 0; a.config.MaxIterations == 0 || iteration < a.config.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("agent run cancelled: %w", err)
		}

		result.Iterations = iteration + 1

		// Build the windowed message slice for the LLM
		windowedMsgs := BuildMessages(history, 50)

		req := llm.ChatRequest{
			Model:     a.config.Model,
			Messages:  windowedMsgs,
			System:    a.config.SystemPrompt,
			Tools:     toolDefs,
			MaxTokens: a.config.MaxTokens,
			Temperature: a.config.Temperature,
		}

		a.logger.Debug().
			Int("iteration", iteration+1).
			Int("messages", len(windowedMsgs)).
			Msg("calling LLM")

		a.emit(AgentEvent{Kind: EventThinking, Iteration: iteration + 1})

		resp, err := a.provider.Chat(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("LLM call failed (iteration %d): %w", iteration+1, err)
		}

		// Emit thinking content if available
		if resp.Thinking != "" {
			a.emit(AgentEvent{Kind: EventThinking, Thinking: resp.Thinking, Iteration: iteration + 1})
		}

		// Track token usage
		a.ctxMgr.RecordUsage(resp.Usage)
		result.Usage.InputTokens += resp.Usage.InputTokens
		result.Usage.OutputTokens += resp.Usage.OutputTokens
		result.StopReason = resp.StopReason

		// Persist assistant message
		a.persistMessage(ctx, sessionID, "assistant", resp.Content, resp.Usage)

		// No tool calls -> final response
		if len(resp.ToolCalls) == 0 {
			result.Response = resp.Content
			result.History = history
			a.emit(AgentEvent{Kind: EventResponse, Content: resp.Content, Iteration: iteration + 1})
			a.logger.Info().
				Int("iterations", result.Iterations).
				Int("tool_calls", result.ToolCalls).
				Msg("agent run complete")
			return result, nil
		}

		// Append assistant message with tool calls to history.
		// Metadata carries provider-specific data (e.g., Gemini thought signatures)
		// that must be round-tripped back in the next request.
		assistantMsg := llm.ChatMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
			Metadata:  resp.Metadata,
		}
		history = append(history, assistantMsg)

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			result.ToolCalls++

			toolResult := a.executeTool(ctx, sessionID, tc)

			// Truncate large outputs
			truncated, wasTruncated := a.ctxMgr.TruncateToolOutput(toolResult.Content)
			if wasTruncated {
				a.logger.Warn().
					Str("tool", tc.Name).
					Int("original_len", len(toolResult.Content)).
					Msg("tool output truncated")
				toolResult.Content = truncated
			}

			// Persist tool result as a message so history can reconstruct tool calls
			a.persistToolMessage(ctx, sessionID, tc.ID, tc.Name, string(tc.Input), toolResult.Content)

			// Append tool result to history
			toolMsg := llm.ChatMessage{
				Role: "tool",
				ToolResult: &llm.ToolResult{
					ToolUseID: tc.ID,
					Content:   toolResult.Content,
					IsError:   toolResult.IsError,
					Parts:     toolResult.Parts,
				},
			}
			history = append(history, toolMsg)
		}

		// Check if we need summarization
		if a.ctxMgr.NeedsSummarization() {
			a.logger.Warn().
				Int("input_tokens", a.ctxMgr.totalInputTokens).
				Int("budget", a.ctxMgr.Budget()).
				Msg("approaching context budget")
		}
	}

	// Max iterations reached — return whatever we have
	result.Response = "[max iterations reached]"
	result.History = history
	result.StopReason = "max_iterations"
	a.logger.Warn().
		Int("max_iterations", a.config.MaxIterations).
		Msg("agent hit iteration limit")
	return result, nil
}

// buildToolDefs converts registered tools to LLM ToolDefinitions.
func (a *Agent) buildToolDefs() []llm.ToolDefinition {
	tools := a.tools.List()
	if len(tools) == 0 {
		return nil
	}

	defs := make([]llm.ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Parameters(),
		}
	}
	return defs
}

// executeTool runs a single tool call and logs the execution.
func (a *Agent) executeTool(ctx context.Context, sessionID string, tc llm.ToolCall) ToolResult {
	tool := a.tools.Get(tc.Name)
	if tool == nil {
		a.logger.Error().Str("tool", tc.Name).Msg("tool not found")
		return ToolResult{
			Content: fmt.Sprintf("error: tool %q not found", tc.Name),
			IsError: true,
		}
	}

	a.logger.Debug().
		Str("tool", tc.Name).
		Str("id", tc.ID).
		Msg("executing tool")

	a.emit(AgentEvent{Kind: EventToolStart, ToolName: tc.Name, ToolID: tc.ID, ToolInput: tc.Input})

	start := time.Now()
	result, err := tool.Execute(ctx, tc.Input)
	duration := time.Since(start)

	if err != nil {
		a.logger.Error().
			Err(err).
			Str("tool", tc.Name).
			Dur("duration", duration).
			Msg("tool execution failed")
		result = ToolResult{
			Content: fmt.Sprintf("error: %s", err.Error()),
			IsError: true,
		}
	} else {
		a.logger.Debug().
			Str("tool", tc.Name).
			Dur("duration", duration).
			Int("output_len", len(result.Content)).
			Msg("tool execution complete")
	}

	a.emit(AgentEvent{Kind: EventToolEnd, ToolName: tc.Name, ToolID: tc.ID, Content: result.Content, IsError: result.IsError})

	// Audit log
	a.persistToolExecution(ctx, sessionID, tc, result, duration)

	return result
}

// persistMessage saves a message to the store if available.
func (a *Agent) persistMessage(ctx context.Context, sessionID, role, content string, usage llm.Usage) {
	if a.store == nil || sessionID == "" {
		return
	}

	msg := StoreMessage{
		SessionID:  sessionID,
		Role:       role,
		Content:    content,
		TokenCount: usage.OutputTokens,
	}

	if _, err := a.store.SaveMessage(ctx, msg); err != nil {
		a.logger.Error().Err(err).Msg("failed to persist message")
	}
}

// persistToolMessage saves a tool result as a message so conversation history
// can reconstruct which tools were called and what they returned.
func (a *Agent) persistToolMessage(ctx context.Context, sessionID, toolCallID, toolName, toolInput, content string) {
	if a.store == nil || sessionID == "" {
		return
	}
	msg := StoreMessage{
		SessionID:  sessionID,
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		ToolInput:  toolInput,
	}
	if _, err := a.store.SaveMessage(ctx, msg); err != nil {
		a.logger.Error().Err(err).Msg("failed to persist tool message")
	}
}

// persistToolExecution saves a tool execution record to the store.
func (a *Agent) persistToolExecution(ctx context.Context, sessionID string, tc llm.ToolCall, result ToolResult, duration time.Duration) {
	if a.store == nil || sessionID == "" {
		return
	}

	exec := StoreToolExecution{
		SessionID:  sessionID,
		ToolName:   tc.Name,
		Input:      string(tc.Input),
		Output:     result.Content,
		DurationMs: duration.Milliseconds(),
		Success:    !result.IsError,
	}

	if err := a.store.SaveToolExecution(ctx, exec); err != nil {
		a.logger.Error().Err(err).Msg("failed to persist tool execution")
	}
}
