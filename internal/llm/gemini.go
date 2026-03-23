package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// GeminiProvider implements the Provider interface using the Google GenAI SDK.
type GeminiProvider struct {
	client *genai.Client
	model  string
}

// GeminiConfig holds configuration for the Gemini provider.
type GeminiConfig struct {
	APIKey string
	Model  string // e.g., "gemini-3-flash-preview"
}

// NewGeminiProvider creates a new Gemini provider using the GenAI SDK.
func NewGeminiProvider(ctx context.Context, cfg GeminiConfig) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating gemini client: %w", err)
	}

	model := cfg.Model
	if model == "" {
		model = "gemini-3-flash-preview"
	}

	return &GeminiProvider{
		client: client,
		model:  model,
	}, nil
}

func (g *GeminiProvider) Name() string { return "gemini" }

func (g *GeminiProvider) Models() []ModelInfo {
	return []ModelInfo{
		{ID: "gemini-3-flash-preview", Name: "Gemini 3 Flash Preview", ContextWindow: 1000000},
		{ID: "gemini-2.5-pro-preview-05-06", Name: "Gemini 2.5 Pro", ContextWindow: 1000000},
		{ID: "gemini-2.5-flash-preview-04-17", Name: "Gemini 2.5 Flash", ContextWindow: 1000000},
	}
}

// Chat sends a non-streaming request to Gemini and returns the full response.
func (g *GeminiProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	contents := convertMessages(req.Messages)
	config := buildConfig(req)

	result, err := g.client.Models.GenerateContent(ctx, g.resolveModel(req.Model), contents, config)
	if err != nil {
		return nil, fmt.Errorf("gemini generate content: %w", err)
	}

	return extractResponse(result)
}

// Stream sends a streaming request to Gemini and returns a channel of chunks.
func (g *GeminiProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	contents := convertMessages(req.Messages)
	config := buildConfig(req)

	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)

		for resp, err := range g.client.Models.GenerateContentStream(ctx, g.resolveModel(req.Model), contents, config) {
			if err != nil {
				ch <- StreamChunk{Err: err}
				return
			}

			chunk := extractStreamChunk(resp)
			ch <- chunk

			if chunk.Done {
				return
			}
		}

		ch <- StreamChunk{Done: true}
	}()

	return ch, nil
}

func (g *GeminiProvider) resolveModel(requestModel string) string {
	if requestModel != "" {
		return requestModel
	}
	return g.model
}

// convertMessages translates Capabot ChatMessages to Gemini Content slices.
func convertMessages(messages []ChatMessage) []*genai.Content {
	var contents []*genai.Content
	for _, msg := range messages {
		content := convertMessage(msg)
		if content != nil {
			contents = append(contents, content)
		}
	}
	return contents
}

func convertMessage(msg ChatMessage) *genai.Content {
	role := mapRole(msg.Role)

	// Tool result messages
	if msg.ToolResult != nil {
		return genai.NewContentFromFunctionResponse(
			msg.ToolResult.ToolUseID,
			map[string]any{
				"output":   msg.ToolResult.Content,
				"is_error": msg.ToolResult.IsError,
			},
			role,
		)
	}

	// If the message carries raw Gemini content (round-tripped via Metadata),
	// use it directly. This preserves thought signatures that Gemini requires
	// when continuing after tool calls.
	if rawContent, ok := msg.Metadata.(*genai.Content); ok && rawContent != nil {
		return rawContent
	}

	var parts []*genai.Part

	// Text content
	if msg.Content != "" {
		parts = append(parts, genai.NewPartFromText(msg.Content))
	}

	// Media parts (images, PDFs)
	for _, p := range msg.Parts {
		parts = append(parts, genai.NewPartFromBytes(p.Data, p.MimeType))
	}

	// Tool calls from assistant
	for _, tc := range msg.ToolCalls {
		var args map[string]any
		if tc.Input != nil {
			json.Unmarshal(tc.Input, &args)
		}
		parts = append(parts, genai.NewPartFromFunctionCall(tc.Name, args))
	}

	if len(parts) == 0 {
		return nil
	}

	return genai.NewContentFromParts(parts, role)
}

func mapRole(role string) genai.Role {
	switch role {
	case "assistant":
		return genai.RoleModel
	default:
		return genai.RoleUser
	}
}

// buildConfig translates Capabot ChatRequest options to Gemini config.
func buildConfig(req ChatRequest) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{}

	if req.System != "" {
		config.SystemInstruction = genai.NewContentFromText(req.System, genai.RoleUser)
	}

	if req.MaxTokens > 0 {
		config.MaxOutputTokens = int32(req.MaxTokens)
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		config.Temperature = &temp
	}

	if len(req.StopSeqs) > 0 {
		config.StopSequences = req.StopSeqs
	}

	if len(req.Tools) > 0 {
		config.Tools = convertTools(req.Tools)
	}

	return config
}

// convertTools translates Capabot ToolDefinitions to Gemini FunctionDeclarations.
func convertTools(tools []ToolDefinition) []*genai.Tool {
	var decls []*genai.FunctionDeclaration
	for _, t := range tools {
		decl := &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
		}
		// Use ParametersJsonSchema for raw JSON schema passthrough
		if t.InputSchema != nil {
			var schema any
			if err := json.Unmarshal(t.InputSchema, &schema); err == nil {
				decl.ParametersJsonSchema = schema
			}
		}
		decls = append(decls, decl)
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// extractResponse converts a Gemini response to a Capabot ChatResponse.
func extractResponse(resp *genai.GenerateContentResponse) (*ChatResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil response from gemini")
	}

	result := &ChatResponse{}

	// Extract text and function calls from candidates.
	// Preserve the raw Content for round-tripping (Gemini requires thought
	// signatures to be sent back when continuing after tool calls).
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.Content != nil {
			// Store raw content as Metadata for round-tripping
			result.Metadata = candidate.Content

			for _, part := range candidate.Content.Parts {
				// Skip thinking/reasoning parts — no user-visible content.
				if part.Thought || (part.Text == "" && part.FunctionCall == nil && len(part.ThoughtSignature) > 0) {
					continue
				}
				if part.Text != "" {
					result.Content += part.Text
				}
				if part.FunctionCall != nil {
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					result.ToolCalls = append(result.ToolCalls, ToolCall{
						ID:    part.FunctionCall.ID,
						Name:  part.FunctionCall.Name,
						Input: argsJSON,
					})
				}
			}
		}

		if candidate.FinishReason != "" {
			result.StopReason = string(candidate.FinishReason)
		}
	}

	// Extract usage
	if resp.UsageMetadata != nil {
		result.Usage = Usage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		}
	}

	return result, nil
}

// extractStreamChunk converts a streaming Gemini response to a StreamChunk.
func extractStreamChunk(resp *genai.GenerateContentResponse) StreamChunk {
	chunk := StreamChunk{}

	if resp == nil {
		chunk.Done = true
		return chunk
	}

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				// Skip thinking/reasoning parts — no user-visible content.
				if part.Thought || (part.Text == "" && part.FunctionCall == nil && len(part.ThoughtSignature) > 0) {
					continue
				}
				if part.Text != "" {
					chunk.Delta += part.Text
				}
				if part.FunctionCall != nil {
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					chunk.ToolCall = &ToolCall{
						ID:    part.FunctionCall.ID,
						Name:  part.FunctionCall.Name,
						Input: argsJSON,
					}
				}
			}
		}

		if candidate.FinishReason == "STOP" || candidate.FinishReason == "MAX_TOKENS" {
			chunk.Done = true
		}
	}

	if resp.UsageMetadata != nil {
		chunk.Usage = &Usage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		}
	}

	return chunk
}
