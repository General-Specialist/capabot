package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/memory"
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
	ID                  string
	Model               string
	SystemPrompt        string
	MaxIterations       int
	MaxTokens           int
	Temperature         *float64
	DisableThinking     bool
	SummarizationModel  string // cheap model for condensing tool outputs (empty = dumb truncation)
	Mode                string // active mode name for usage tracking
}

// StoreWriter is the subset of memory.Store the agent needs for audit logging.
type StoreWriter interface {
	SaveMessage(ctx context.Context, msg memory.Message) (int64, error)
	SaveToolExecution(ctx context.Context, exec memory.ToolExecution) error
	SaveUsage(ctx context.Context, rec memory.UsageRecord) error
}

// RunResult is the final outcome of an agent run.
type RunResult struct {
	Response   string            `json:"response"`
	ToolCalls  int               `json:"tool_calls"`
	Iterations int               `json:"iterations"`
	Usage      llm.Usage         `json:"usage"`
	StopReason string            `json:"stop_reason"`
	History    []llm.ChatMessage `json:"-"`
}

// ToolHook intercepts tool execution. Plugins can register pre/post hooks
// to modify parameters, block execution, or transform results.
type ToolHook interface {
	// BeforeToolUse is called before a tool executes. Return allow=false to
	// block execution. Modified params (if non-nil) replace the original.
	BeforeToolUse(ctx context.Context, toolName string, params json.RawMessage) (allow bool, modifiedParams json.RawMessage, err error)

	// AfterToolUse is called after a tool executes. Modified result (if non-nil)
	// replaces the original.
	AfterToolUse(ctx context.Context, toolName string, params json.RawMessage, result json.RawMessage) (modifiedResult json.RawMessage, err error)
}

// Agent implements the ReAct loop: Observe -> Think -> Act -> Observe.
type Agent struct {
	config   AgentConfig
	provider llm.Provider
	tools    *Registry
	ctxMgr   *ContextManager
	store     StoreWriter // nil = no persistence
	usageOnly bool        // if true, only persist usage (not messages)
	logger    zerolog.Logger
	onEvent   func(AgentEvent) // nil = no streaming
	hooks     []ToolHook       // plugin hooks (pre/post tool execution)
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

// SetUsageOnly makes the agent only persist usage records, not messages or tool executions.
func (a *Agent) SetUsageOnly(v bool) {
	a.usageOnly = v
}

// AddHook adds a tool hook that will be called before/after tool execution.
func (a *Agent) AddHook(h ToolHook) {
	a.hooks = append(a.hooks, h)
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

		// Compress old tool outputs before sending to LLM.
		// Current iteration's results stay full; older ones get condensed.
		if iteration > 0 {
			a.compressOldToolOutputs(ctx, history)
		}

		// Build the windowed message slice for the LLM
		windowedMsgs := BuildMessages(history, 50)

		req := llm.ChatRequest{
			Model:           a.config.Model,
			Messages:        windowedMsgs,
			System:          a.config.SystemPrompt,
			Tools:           toolDefs,
			MaxTokens:       a.config.MaxTokens,
			Temperature:     a.config.Temperature,
			DisableThinking: a.config.DisableThinking,
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

		// Log usage for cost tracking
		a.persistUsage(ctx, resp)
		result.StopReason = resp.StopReason

		// Persist assistant message
		a.persistMessage(ctx, sessionID, "assistant", resp.Content, resp.Usage)

		// No tool calls -> final response (retry once if empty)
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) == "" {
				a.logger.Warn().Int("iteration", iteration+1).Msg("empty response, retrying")
				history = append(history, llm.ChatMessage{Role: "user", Content: "Please provide a response."})
				continue
			}
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

			// Persist full output before any truncation
			a.persistToolMessage(ctx, sessionID, tc.ID, tc.Name, string(tc.Input), toolResult.Content)

			// Truncate extremely large outputs (hard cap)
			truncated, wasTruncated := a.ctxMgr.TruncateToolOutput(toolResult.Content)
			if wasTruncated {
				a.logger.Warn().
					Str("tool", tc.Name).
					Int("original_len", len(toolResult.Content)).
					Msg("tool output truncated")
				toolResult.Content = truncated
			}

			// Append tool result to history (full output for current iteration —
			// will be compressed on the next iteration before sending to LLM)
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

// compressThreshold is the minimum content length before compression kicks in.
// Short outputs like "edited /path/to/file" are already compact.
const compressThreshold = 300

// compressOldToolOutputs replaces tool result content in history with compact
// summaries, except for the most recent batch of tool results (from the last
// assistant message with tool calls). Uses a cheap model if configured,
// otherwise falls back to simple truncation.
func (a *Agent) compressOldToolOutputs(ctx context.Context, history []llm.ChatMessage) {
	// Find the index of the last assistant message with tool calls.
	// Everything after it is "current" tool results — keep those full.
	lastAssistantIdx := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" && len(history[i].ToolCalls) > 0 {
			lastAssistantIdx = i
			break
		}
	}

	for i := 0; i < lastAssistantIdx; i++ {
		tr := history[i].ToolResult
		if tr == nil || len(tr.Content) <= compressThreshold {
			continue
		}
		tr.Content = a.summarizeOutput(ctx, tr.Content)
		tr.Parts = nil // drop media from old results
	}
}

// summarizeOutput condenses a tool output. Uses the summarization model if
// configured, otherwise falls back to simple truncation.
func (a *Agent) summarizeOutput(ctx context.Context, content string) string {
	if a.config.SummarizationModel != "" {
		summary, err := a.llmSummarize(ctx, content)
		if err == nil && summary != "" {
			return summary
		}
		a.logger.Debug().Err(err).Msg("summarization model failed, falling back to truncation")
	}
	return truncateOutput(content)
}

// llmSummarize calls the cheap model to produce a concise summary.
func (a *Agent) llmSummarize(ctx context.Context, content string) (string, error) {
	// Cap what we send to the summarizer to avoid blowing its context
	input := content
	if len(input) > 8000 {
		input = input[:8000]
	}

	resp, err := a.provider.Chat(ctx, llm.ChatRequest{
		Model:           a.config.SummarizationModel,
		System:          "Summarize this tool output in 1-3 sentences. Focus on the key result: success/failure, what was found/changed, important data. Be terse.",
		Messages:        []llm.ChatMessage{{Role: "user", Content: input}},
		MaxTokens:       256,
		DisableThinking: true,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// truncateOutput is the dumb fallback: first 2 lines + stats.
func truncateOutput(content string) string {
	lines := strings.Count(content, "\n") + 1
	chars := len(content)

	preview := content
	if idx := strings.Index(content, "\n"); idx >= 0 {
		if idx2 := strings.Index(content[idx+1:], "\n"); idx2 >= 0 {
			preview = content[:idx+1+idx2]
		} else {
			preview = content[:idx]
		}
	}
	if len(preview) > 200 {
		preview = preview[:200]
	}

	return fmt.Sprintf("%s\n[condensed: %d lines, %d chars — re-run tool for full output]", preview, lines, chars)
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

	// Run pre-hooks — any hook can block execution or modify params.
	params := tc.Input
	for _, h := range a.hooks {
		allow, modified, err := h.BeforeToolUse(ctx, tc.Name, params)
		if err != nil {
			a.logger.Warn().Err(err).Str("tool", tc.Name).Msg("pre-hook error")
			continue
		}
		if !allow {
			a.logger.Info().Str("tool", tc.Name).Msg("tool blocked by hook")
			return ToolResult{
				Content: fmt.Sprintf("tool %q was blocked by a plugin hook", tc.Name),
				IsError: true,
			}
		}
		if modified != nil {
			params = modified
		}
	}

	a.logger.Debug().
		Str("tool", tc.Name).
		Str("id", tc.ID).
		Msg("executing tool")

	a.emit(AgentEvent{Kind: EventToolStart, ToolName: tc.Name, ToolID: tc.ID, ToolInput: params})

	start := time.Now()
	result, err := tool.Execute(ctx, params)
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

	// Run post-hooks — can modify the result.
	for _, h := range a.hooks {
		resultJSON, _ := json.Marshal(result)
		modified, err := h.AfterToolUse(ctx, tc.Name, params, resultJSON)
		if err != nil {
			a.logger.Warn().Err(err).Str("tool", tc.Name).Msg("post-hook error")
			continue
		}
		if modified != nil {
			var modResult ToolResult
			if json.Unmarshal(modified, &modResult) == nil {
				result = modResult
			}
		}
	}

	a.emit(AgentEvent{Kind: EventToolEnd, ToolName: tc.Name, ToolID: tc.ID, Content: result.Content, IsError: result.IsError})

	// Audit log
	a.persistToolExecution(ctx, sessionID, tc, result, duration)

	return result
}

// persistMessage saves a message to the store if available.
func (a *Agent) persistMessage(ctx context.Context, sessionID, role, content string, usage llm.Usage) {
	if a.store == nil || a.usageOnly || sessionID == "" {
		return
	}

	msg := memory.Message{
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
	if a.store == nil || a.usageOnly || sessionID == "" {
		return
	}
	msg := memory.Message{
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
	if a.store == nil || a.usageOnly || sessionID == "" {
		return
	}

	exec := memory.ToolExecution{
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


// persistUsage logs an LLM call for cost tracking.
func (a *Agent) persistUsage(ctx context.Context, resp *llm.ChatResponse) {
	cost := llm.EstimateCost(resp.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	logEvt := a.logger.Info().
		Str("provider", resp.Provider).
		Str("model", resp.Model).
		Int("input_tokens", resp.Usage.InputTokens).
		Int("output_tokens", resp.Usage.OutputTokens)
	if cost > 0 {
		logEvt = logEvt.Str("cost", fmt.Sprintf("$%.4f", cost))
	}
	logEvt.Msg("llm call")

	if a.store == nil {
		return
	}
	mode := a.config.Mode
	if mode == "" {
		mode = "default"
	}
	if err := a.store.SaveUsage(ctx, memory.UsageRecord{
		Provider:     resp.Provider,
		Model:        resp.Model,
		Mode:         mode,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
	}); err != nil {
		a.logger.Error().Err(err).Msg("failed to persist usage")
	}
}
