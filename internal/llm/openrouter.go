package llm

import "context"

const openRouterBaseURL = "https://openrouter.ai/api/v1"

// OpenRouterConfig holds configuration for the OpenRouter provider.
type OpenRouterConfig struct {
	APIKey string
	// Model is the OpenRouter model ID, e.g. "anthropic/claude-sonnet-4-6".
	// Defaults to "anthropic/claude-sonnet-4-6".
	Model string
	// AppName is sent in the X-Title header (optional, for OpenRouter leaderboard).
	AppName string
	// SiteURL is sent in the HTTP-Referer header (optional).
	SiteURL string
}

// OpenRouterProvider wraps OpenAIProvider with OpenRouter-specific defaults.
// It routes requests through openrouter.ai, giving access to 100+ models from
// Anthropic, OpenAI, Google, Meta, Mistral, and others via a single API key.
type OpenRouterProvider struct {
	inner   *OpenAIProvider
	appName string
	siteURL string
}

// NewOpenRouterProvider creates a provider that routes LLM calls through OpenRouter.
func NewOpenRouterProvider(cfg OpenRouterConfig) *OpenRouterProvider {
	model := cfg.Model
	if model == "" {
		model = "anthropic/claude-sonnet-4-6"
	}
	inner := NewOpenAIProvider(OpenAIConfig{
		APIKey:  cfg.APIKey,
		Model:   model,
		BaseURL: openRouterBaseURL,
	})
	return &OpenRouterProvider{
		inner:   inner,
		appName: cfg.AppName,
		siteURL: cfg.SiteURL,
	}
}

func (o *OpenRouterProvider) Name() string { return "openrouter" }

func (o *OpenRouterProvider) Models() []ModelInfo {
	// A curated subset of popular OpenRouter models.
	// The full list is available via the OpenRouter /models API.
	return []ModelInfo{
		{ID: "anthropic/claude-sonnet-4-6", Name: "Claude Sonnet 4.6 (via OpenRouter)"},
		{ID: "anthropic/claude-opus-4-6", Name: "Claude Opus 4.6 (via OpenRouter)"},
		{ID: "openai/gpt-4o", Name: "GPT-4o (via OpenRouter)"},
		{ID: "openai/gpt-4o-mini", Name: "GPT-4o Mini (via OpenRouter)"},
		{ID: "google/gemini-2.0-flash-001", Name: "Gemini 2.0 Flash (via OpenRouter)"},
		{ID: "meta-llama/llama-3.3-70b-instruct", Name: "Llama 3.3 70B (via OpenRouter)"},
		{ID: "mistralai/mistral-large-2411", Name: "Mistral Large (via OpenRouter)"},
		{ID: "qwen/qwen-2.5-72b-instruct", Name: "Qwen 2.5 72B (via OpenRouter)"},
	}
}

// Chat delegates to the inner OpenAI-compat provider, injecting OpenRouter headers.
func (o *OpenRouterProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return o.inner.chatWithHeaders(ctx, req, o.extraHeaders())
}

// Stream delegates to the inner OpenAI-compat provider, injecting OpenRouter headers.
func (o *OpenRouterProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	return o.inner.streamWithHeaders(ctx, req, o.extraHeaders())
}

func (o *OpenRouterProvider) extraHeaders() map[string]string {
	h := make(map[string]string)
	if o.siteURL != "" {
		h["HTTP-Referer"] = o.siteURL
	}
	if o.appName != "" {
		h["X-Title"] = o.appName
	}
	return h
}
