package llm

import (
	"context"
	"fmt"
)

// RouterConfig defines primary and fallback providers for the router.
type RouterConfig struct {
	Primary   string   // provider name: "anthropic", "openai", "gemini"
	Fallbacks []string // tried in order on retryable errors
}

// Router implements Provider by delegating to a primary provider and falling
// back to alternatives on retryable errors (429 / 5xx).
type Router struct {
	providers map[string]Provider
	config    RouterConfig
}

// NewRouter creates a new Router with the given configuration and provider map.
func NewRouter(cfg RouterConfig, providers map[string]Provider) *Router {
	return &Router{
		providers: providers,
		config:    cfg,
	}
}

func (r *Router) Name() string { return "router" }

// ProviderMap returns a copy of the underlying provider map.
func (r *Router) ProviderMap() map[string]Provider {
	out := make(map[string]Provider, len(r.providers))
	for k, v := range r.providers {
		out[k] = v
	}
	return out
}

// Models returns the union of all providers' model lists.
func (r *Router) Models() []ModelInfo {
	seen := make(map[string]bool)
	var result []ModelInfo
	for _, p := range r.providers {
		for _, m := range p.Models() {
			if !seen[m.ID] {
				seen[m.ID] = true
				result = append(result, m)
			}
		}
	}
	return result
}

// Chat delegates to the primary provider, falling back on retryable errors.
func (r *Router) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	names := r.orderedProviders()
	var lastErr error
	for _, name := range names {
		p, ok := r.providers[name]
		if !ok {
			continue
		}
		resp, err := p.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, fmt.Errorf("router chat: provider %q: %w", name, err)
		}
	}
	return nil, fmt.Errorf("router chat: all providers failed: %w", lastErr)
}

// Stream delegates to the primary provider, falling back on retryable errors
// before any chunks are sent.
func (r *Router) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	names := r.orderedProviders()
	var lastErr error
	for _, name := range names {
		p, ok := r.providers[name]
		if !ok {
			continue
		}
		ch, err := p.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, fmt.Errorf("router stream: provider %q: %w", name, err)
		}
	}
	return nil, fmt.Errorf("router stream: all providers failed: %w", lastErr)
}

// orderedProviders returns [primary, fallbacks...].
func (r *Router) orderedProviders() []string {
	result := make([]string, 0, 1+len(r.config.Fallbacks))
	result = append(result, r.config.Primary)
	result = append(result, r.config.Fallbacks...)
	return result
}
