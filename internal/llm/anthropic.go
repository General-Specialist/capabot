package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AnthropicProvider implements the Provider interface using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// AnthropicConfig holds configuration for the Anthropic provider.
type AnthropicConfig struct {
	APIKey  string
	Model   string // default: "claude-sonnet-4-6"
	BaseURL string // default: "https://api.anthropic.com"
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(cfg AnthropicConfig) *AnthropicProvider {
	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicProvider{
		apiKey:     cfg.APIKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

func (a *AnthropicProvider) Name() string { return "anthropic" }

func (a *AnthropicProvider) Models() []ModelInfo {
	return []ModelInfo{
		{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", ContextWindow: 200000},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", ContextWindow: 200000},
		{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", ContextWindow: 200000},
	}
}

// anthropicRequest is the wire format for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string                   `json:"model"`
	Messages  []anthropicMessage       `json:"messages"`
	System    any                      `json:"system,omitempty"` // string or []anthropicSystemBlock
	MaxTokens int                      `json:"max_tokens"`
	Tools     []anthropicTool          `json:"tools,omitempty"`
	Stream    bool                     `json:"stream,omitempty"`
	Thinking  *anthropicThinkingConfig `json:"thinking,omitempty"`
}

// anthropicSystemBlock is a structured system prompt block with optional cache control.
type anthropicSystemBlock struct {
	Type         string                  `json:"type"`
	Text         string                  `json:"text"`
	CacheControl *anthropicCacheControl  `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type anthropicThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	Thinking  string                 `json:"thinking,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     json.RawMessage        `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   any                    `json:"content,omitempty"` // string or []anthropicContentBlock
	IsError   bool                   `json:"is_error,omitempty"`
	Source    *anthropicMediaSource  `json:"source,omitempty"`
}

type anthropicMediaSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/jpeg", "application/pdf", etc.
	Data      string `json:"data"`       // base64-encoded
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicResponse is the wire format for the Anthropic Messages API response.
type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Chat sends a non-streaming request to Anthropic and returns the full response.
func (a *AnthropicProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(a.buildRequest(req, false))
	if err != nil {
		return nil, fmt.Errorf("anthropic chat: marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic chat: creating request: %w", err)
	}
	a.setHeaders(httpReq)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat: sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("anthropic chat: %w", httpStatusError(resp))
	}

	var apiResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("anthropic chat: decoding response: %w", err)
	}

	return extractAnthropicResponse(apiResp), nil
}

// Stream sends a streaming request to Anthropic and returns a channel of chunks.
func (a *AnthropicProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	body, err := json.Marshal(a.buildRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("anthropic stream: marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic stream: creating request: %w", err)
	}
	a.setHeaders(httpReq)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream: sending request: %w", err)
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic stream: %w", httpStatusError(resp))
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		parseAnthropicSSE(resp.Body, ch)
	}()

	return ch, nil
}

func (a *AnthropicProvider) resolveModel(requestModel string) string {
	if requestModel != "" {
		return requestModel
	}
	return a.model
}

func (a *AnthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	req.Header.Set("content-type", "application/json")
}

func (a *AnthropicProvider) buildRequest(req ChatRequest, stream bool) anthropicRequest {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	apiReq := anthropicRequest{
		Model:     a.resolveModel(req.Model),
		Messages:  convertAnthropicMessages(req.Messages),
		MaxTokens: maxTokens,
		Stream:    stream,
	}
	if req.System != "" {
		// Send system prompt as a structured block with cache_control.
		// If tools are present, cache_control goes on the last tool instead (larger cache).
		if len(req.Tools) > 0 {
			apiReq.System = []anthropicSystemBlock{{Type: "text", Text: req.System}}
		} else {
			apiReq.System = []anthropicSystemBlock{{
				Type:         "text",
				Text:         req.System,
				CacheControl: &anthropicCacheControl{Type: "ephemeral"},
			}}
		}
	}
	if len(req.Tools) > 0 {
		apiReq.Tools = convertAnthropicTools(req.Tools)
		// Mark the last tool with cache_control so system + tools are all cached.
		apiReq.Tools[len(apiReq.Tools)-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	// Enable extended thinking unless explicitly disabled (e.g. chat mode).
	if !req.DisableThinking {
		budget := maxTokens * 80 / 100
		if budget < 1024 {
			budget = 1024
		}
		if apiReq.MaxTokens < budget+1024 {
			apiReq.MaxTokens = budget + 4096
		}
		apiReq.Thinking = &anthropicThinkingConfig{
			Type:         "enabled",
			BudgetTokens: budget,
		}
	}

	return apiReq
}

// convertAnthropicMessages converts ChatMessages to Anthropic wire format.
func convertAnthropicMessages(messages []ChatMessage) []anthropicMessage {
	result := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		result = append(result, convertAnthropicMessage(msg))
	}
	return result
}

func convertAnthropicMessage(msg ChatMessage) anthropicMessage {
	// Tool result: user message with tool_result block
	if msg.ToolResult != nil {
		tr := msg.ToolResult
		var content any
		if len(tr.Parts) > 0 {
			// Multimodal tool result — content is an array of blocks
			blocks := make([]anthropicContentBlock, 0, len(tr.Parts)+1)
			if tr.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: tr.Content})
			}
			for _, p := range tr.Parts {
				blockType := "image"
				if p.MimeType == "application/pdf" {
					blockType = "document"
				}
				blocks = append(blocks, anthropicContentBlock{
					Type: blockType,
					Source: &anthropicMediaSource{
						Type:      "base64",
						MediaType: p.MimeType,
						Data:      base64.StdEncoding.EncodeToString(p.Data),
					},
				})
			}
			content = blocks
		} else {
			content = tr.Content
		}
		block := anthropicContentBlock{
			Type:      "tool_result",
			ToolUseID: tr.ToolUseID,
			Content:   content,
			IsError:   tr.IsError,
		}
		return anthropicMessage{Role: "user", Content: []anthropicContentBlock{block}}
	}

	// Assistant message with tool calls
	if len(msg.ToolCalls) > 0 {
		blocks := make([]anthropicContentBlock, 0, len(msg.ToolCalls)+1)
		if msg.Content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			input := tc.Input
			if input == nil {
				input = json.RawMessage("{}")
			}
			blocks = append(blocks, anthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: input,
			})
		}
		return anthropicMessage{Role: "assistant", Content: blocks}
	}

	// User message — may include media parts
	role := msg.Role
	if role == "tool" {
		role = "user"
	}
	if len(msg.Parts) > 0 {
		blocks := make([]anthropicContentBlock, 0, len(msg.Parts)+1)
		for _, p := range msg.Parts {
			blockType := "image"
			if p.MimeType == "application/pdf" {
				blockType = "document"
			}
			blocks = append(blocks, anthropicContentBlock{
				Type: blockType,
				Source: &anthropicMediaSource{
					Type:      "base64",
					MediaType: p.MimeType,
					Data:      base64.StdEncoding.EncodeToString(p.Data),
				},
			})
		}
		if msg.Content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: msg.Content})
		}
		return anthropicMessage{Role: role, Content: blocks}
	}
	return anthropicMessage{Role: role, Content: msg.Content}
}

// convertAnthropicTools converts ToolDefinitions to Anthropic tool format.
func convertAnthropicTools(tools []ToolDefinition) []anthropicTool {
	result := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		result = append(result, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return result
}

// extractAnthropicResponse converts the Anthropic API response to a ChatResponse.
func extractAnthropicResponse(apiResp anthropicResponse) *ChatResponse {
	result := &ChatResponse{
		StopReason: apiResp.StopReason,
		Usage: Usage{
			InputTokens:  apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
		},
	}

	for _, block := range apiResp.Content {
		switch block.Type {
		case "thinking":
			result.Thinking += block.Thinking
		case "text":
			result.Content += block.Text
		case "tool_use":
			input := block.Input
			if input == nil {
				input = json.RawMessage("{}")
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:    block.ID,
				Name:  block.Name,
				Input: input,
			})
		}
	}

	return result
}

// anthropicSSEState tracks streaming state for tool call assembly.
type anthropicSSEState struct {
	currentToolID   string
	currentToolName string
	inputJSONBuf    strings.Builder
	inToolBlock     bool
	inThinkingBlock bool
}

// parseAnthropicSSE reads SSE events from the response body and sends StreamChunks to ch.
func parseAnthropicSSE(body io.Reader, ch chan<- StreamChunk) {
	scanner := bufio.NewScanner(body)
	state := &anthropicSSEState{}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		var eventType string
		if raw, ok := event["type"]; ok {
			json.Unmarshal(raw, &eventType)
		}

		switch eventType {
		case "content_block_start":
			handleAnthropicBlockStart(event, state)

		case "content_block_delta":
			chunk := handleAnthropicBlockDelta(event, state)
			if chunk != nil {
				ch <- *chunk
			}

		case "content_block_stop":
			if state.inThinkingBlock {
				state.inThinkingBlock = false
			}
			if state.inToolBlock {
				input := json.RawMessage(state.inputJSONBuf.String())
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				ch <- StreamChunk{
					ToolCall: &ToolCall{
						ID:    state.currentToolID,
						Name:  state.currentToolName,
						Input: input,
					},
				}
				state.inToolBlock = false
				state.currentToolID = ""
				state.currentToolName = ""
				state.inputJSONBuf.Reset()
			}

		case "message_delta":
			chunk := handleAnthropicMessageDelta(event)
			if chunk != nil {
				ch <- *chunk
			}

		case "message_stop":
			ch <- StreamChunk{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamChunk{Err: fmt.Errorf("anthropic stream: reading SSE: %w", err)}
		return
	}

	ch <- StreamChunk{Done: true}
}

func handleAnthropicBlockStart(event map[string]json.RawMessage, state *anthropicSSEState) {
	raw, ok := event["content_block"]
	if !ok {
		return
	}
	var block anthropicContentBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return
	}
	switch block.Type {
	case "tool_use":
		state.inToolBlock = true
		state.currentToolID = block.ID
		state.currentToolName = block.Name
		state.inputJSONBuf.Reset()
	case "thinking":
		state.inThinkingBlock = true
	}
}

func handleAnthropicBlockDelta(event map[string]json.RawMessage, state *anthropicSSEState) *StreamChunk {
	raw, ok := event["delta"]
	if !ok {
		return nil
	}
	var delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	}
	if err := json.Unmarshal(raw, &delta); err != nil {
		return nil
	}

	switch delta.Type {
	case "thinking_delta":
		if delta.Thinking != "" {
			return &StreamChunk{Thinking: delta.Thinking}
		}
	case "text_delta":
		if delta.Text != "" {
			return &StreamChunk{Delta: delta.Text}
		}
	case "input_json_delta":
		state.inputJSONBuf.WriteString(delta.PartialJSON)
	}
	return nil
}

func handleAnthropicMessageDelta(event map[string]json.RawMessage) *StreamChunk {
	usageRaw, ok := event["usage"]
	if !ok {
		return nil
	}
	var usage struct {
		OutputTokens int `json:"output_tokens"`
	}
	if err := json.Unmarshal(usageRaw, &usage); err != nil {
		return nil
	}
	if usage.OutputTokens > 0 {
		return &StreamChunk{
			Usage: &Usage{OutputTokens: usage.OutputTokens},
		}
	}
	return nil
}
