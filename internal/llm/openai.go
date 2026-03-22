package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAIProvider implements the Provider interface using the OpenAI chat completions API.
type OpenAIProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// OpenAIConfig holds configuration for the OpenAI-compatible provider.
type OpenAIConfig struct {
	APIKey  string
	Model   string
	BaseURL string // e.g., "https://api.openai.com" or an OpenRouter URL
}

// NewOpenAIProvider creates a new OpenAI-compatible provider.
func NewOpenAIProvider(cfg OpenAIConfig) *OpenAIProvider {
	model := cfg.Model
	if model == "" {
		model = "gpt-4o"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIProvider{
		apiKey:     cfg.APIKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

func (o *OpenAIProvider) Name() string { return "openai" }

func (o *OpenAIProvider) Models() []ModelInfo {
	return []ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
		{ID: "gpt-4o-mini", Name: "GPT-4o Mini", ContextWindow: 128000},
		{ID: "gpt-4-turbo", Name: "GPT-4 Turbo", ContextWindow: 128000},
	}
}

// openAIRequest is the wire format for the OpenAI chat completions API.
type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
	Tools     []openAITool    `json:"tools,omitempty"`
	Stream    bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// openAIResponse is the wire format for the OpenAI chat completions response.
type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Chat sends a non-streaming request to the OpenAI-compatible endpoint.
func (o *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return o.chatWithHeaders(ctx, req, nil)
}

// chatWithHeaders is the internal implementation that accepts extra HTTP headers.
// Used by OpenRouterProvider to inject X-Title / HTTP-Referer.
func (o *OpenAIProvider) chatWithHeaders(ctx context.Context, req ChatRequest, extra map[string]string) (*ChatResponse, error) {
	body, err := json.Marshal(o.buildRequest(req, false))
	if err != nil {
		return nil, fmt.Errorf("openai chat: marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai chat: creating request: %w", err)
	}
	o.setHeaders(httpReq)
	for k, v := range extra {
		httpReq.Header.Set(k, v)
	}

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai chat: sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai chat: %w", httpStatusError(resp))
	}

	var apiResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openai chat: decoding response: %w", err)
	}

	return extractOpenAIResponse(apiResp), nil
}

// Stream sends a streaming request to the OpenAI-compatible endpoint.
func (o *OpenAIProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	return o.streamWithHeaders(ctx, req, nil)
}

// streamWithHeaders is the internal implementation that accepts extra HTTP headers.
func (o *OpenAIProvider) streamWithHeaders(ctx context.Context, req ChatRequest, extra map[string]string) (<-chan StreamChunk, error) {
	body, err := json.Marshal(o.buildRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("openai stream: marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai stream: creating request: %w", err)
	}
	o.setHeaders(httpReq)
	for k, v := range extra {
		httpReq.Header.Set(k, v)
	}

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai stream: sending request: %w", err)
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("openai stream: %w", httpStatusError(resp))
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		parseOpenAISSE(resp.Body, ch)
	}()

	return ch, nil
}

func (o *OpenAIProvider) resolveModel(requestModel string) string {
	if requestModel != "" {
		return requestModel
	}
	return o.model
}

func (o *OpenAIProvider) setHeaders(req *http.Request) {
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
}

func (o *OpenAIProvider) buildRequest(req ChatRequest, stream bool) openAIRequest {
	messages := convertOpenAIMessages(req.Messages, req.System)

	apiReq := openAIRequest{
		Model:    o.resolveModel(req.Model),
		Messages: messages,
		Stream:   stream,
	}
	if req.MaxTokens > 0 {
		apiReq.MaxTokens = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		apiReq.Tools = convertOpenAITools(req.Tools)
	}
	return apiReq
}

// convertOpenAIMessages converts ChatMessages to OpenAI wire format, prepending system if set.
func convertOpenAIMessages(messages []ChatMessage, system string) []openAIMessage {
	result := make([]openAIMessage, 0, len(messages)+1)
	if system != "" {
		result = append(result, openAIMessage{Role: "system", Content: system})
	}
	for _, msg := range messages {
		result = append(result, convertOpenAIMessage(msg))
	}
	return result
}

func convertOpenAIMessage(msg ChatMessage) openAIMessage {
	// Tool result message
	if msg.ToolResult != nil {
		return openAIMessage{
			Role:       "tool",
			ToolCallID: msg.ToolResult.ToolUseID,
			Content:    msg.ToolResult.Content,
		}
	}

	// Assistant message with tool calls
	if len(msg.ToolCalls) > 0 {
		calls := make([]openAIToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			args := string(tc.Input)
			if args == "" {
				args = "{}"
			}
			call := openAIToolCall{
				ID:   tc.ID,
				Type: "function",
			}
			call.Function.Name = tc.Name
			call.Function.Arguments = args
			calls = append(calls, call)
		}
		return openAIMessage{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: calls,
		}
	}

	role := msg.Role
	if role == "tool" {
		role = "assistant"
	}
	return openAIMessage{Role: role, Content: msg.Content}
}

// convertOpenAITools converts ToolDefinitions to OpenAI tool format.
func convertOpenAITools(tools []ToolDefinition) []openAITool {
	result := make([]openAITool, 0, len(tools))
	for _, t := range tools {
		params := t.InputSchema
		if params == nil {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tool := openAITool{Type: "function"}
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = params
		result = append(result, tool)
	}
	return result
}

// extractOpenAIResponse converts the OpenAI API response to a ChatResponse.
func extractOpenAIResponse(apiResp openAIResponse) *ChatResponse {
	result := &ChatResponse{
		Usage: Usage{
			InputTokens:  apiResp.Usage.PromptTokens,
			OutputTokens: apiResp.Usage.CompletionTokens,
		},
	}

	if len(apiResp.Choices) == 0 {
		return result
	}

	choice := apiResp.Choices[0]
	result.Content = choice.Message.Content
	result.StopReason = mapOpenAIFinishReason(choice.FinishReason)

	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	return result
}

func mapOpenAIFinishReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return reason
	}
}

// openAIStreamState tracks accumulated tool call data across SSE deltas.
type openAIStreamState struct {
	toolCalls map[int]*openAIToolCall
	content   strings.Builder
}

// parseOpenAISSE reads SSE events from the response body and sends StreamChunks to ch.
func parseOpenAISSE(body io.Reader, ch chan<- StreamChunk) {
	scanner := bufio.NewScanner(body)
	state := &openAIStreamState{
		toolCalls: make(map[int]*openAIToolCall),
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Flush any accumulated tool calls
			flushOpenAIToolCalls(state, ch)
			ch <- StreamChunk{Done: true}
			return
		}

		var event struct {
			Choices []struct {
				Delta struct {
					Content   string           `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Choices) == 0 {
			if event.Usage != nil {
				ch <- StreamChunk{
					Usage: &Usage{
						InputTokens:  event.Usage.PromptTokens,
						OutputTokens: event.Usage.CompletionTokens,
					},
				}
			}
			continue
		}

		choice := event.Choices[0]

		// Accumulate text delta
		if choice.Delta.Content != "" {
			ch <- StreamChunk{Delta: choice.Delta.Content}
		}

		// Accumulate tool call deltas
		for _, tcDelta := range choice.Delta.ToolCalls {
			existing, ok := state.toolCalls[tcDelta.Index]
			if !ok {
				existing = &openAIToolCall{}
				state.toolCalls[tcDelta.Index] = existing
			}
			if tcDelta.ID != "" {
				existing.ID = tcDelta.ID
			}
			if tcDelta.Type != "" {
				existing.Type = tcDelta.Type
			}
			if tcDelta.Function.Name != "" {
				existing.Function.Name += tcDelta.Function.Name
			}
			if tcDelta.Function.Arguments != "" {
				existing.Function.Arguments += tcDelta.Function.Arguments
			}
		}

		if choice.FinishReason == "tool_calls" || choice.FinishReason == "stop" {
			flushOpenAIToolCalls(state, ch)
			ch <- StreamChunk{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamChunk{Err: fmt.Errorf("openai stream: reading SSE: %w", err)}
		return
	}

	flushOpenAIToolCalls(state, ch)
	ch <- StreamChunk{Done: true}
}

func flushOpenAIToolCalls(state *openAIStreamState, ch chan<- StreamChunk) {
	for i := 0; i < len(state.toolCalls); i++ {
		tc, ok := state.toolCalls[i]
		if !ok {
			continue
		}
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		ch <- StreamChunk{
			ToolCall: &ToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(args),
			},
		}
	}
}
