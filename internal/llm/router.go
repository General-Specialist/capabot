package llm

import (
	"context"
	"fmt"
	"sync"
)

// RouterConfig defines primary and fallback providers for the router.
type RouterConfig struct {
	Primary   string   // provider name: "anthropic", "openai", "gemini"
	Fallbacks []string // tried in order on retryable errors
}

// Router implements Provider by delegating to a primary provider and falling
// back to alternatives on retryable errors (429 / 5xx).
type Router struct {
	mu        sync.RWMutex
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

// SetProvider adds or replaces a provider by name, thread-safe.
func (r *Router) SetProvider(name string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		delete(r.providers, name)
	} else {
		r.providers[name] = p
		if r.config.Primary == "" || r.config.Primary == "anthropic" && name != "anthropic" {
			r.config.Primary = name
		}
	}
}

// ProviderMap returns a copy of the underlying provider map.
func (r *Router) ProviderMap() map[string]Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Provider, len(r.providers))
	for k, v := range r.providers {
		out[k] = v
	}
	return out
}

// Models returns the union of all providers' model lists.
func (r *Router) Models() []ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
		r.mu.RLock()
		p, ok := r.providers[name]
		r.mu.RUnlock()
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
		r.mu.RLock()
		p, ok := r.providers[name]
		r.mu.RUnlock()
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
	r.mu.RLock()
	primary := r.config.Primary
	fallbacks := append([]string(nil), r.config.Fallbacks...)
	r.mu.RUnlock()
	result := make([]string, 0, 1+len(fallbacks))
	result = append(result, primary)
	result = append(result, fallbacks...)
	return result
}
