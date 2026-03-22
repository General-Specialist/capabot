package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/polymath/capabot/internal/agent"
)

// WebSearchTool implements the web_search tool using configurable backends.
type WebSearchTool struct {
	backend    string // "brave", "searxng", "duckduckgo"
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// WebSearchConfig configures the web_search tool.
type WebSearchConfig struct {
	Backend string // "brave" | "searxng" | "duckduckgo" (default)
	APIKey  string // Brave API key
	BaseURL string // SearXNG base URL
}

// NewWebSearchTool creates a new web_search tool.
func NewWebSearchTool(cfg WebSearchConfig) *WebSearchTool {
	return NewWebSearchToolWithURL(cfg, "")
}

// NewWebSearchToolWithURL creates a web_search tool with a custom base URL for testing.
func NewWebSearchToolWithURL(cfg WebSearchConfig, overrideURL string) *WebSearchTool {
	backend := cfg.Backend
	if backend == "" {
		backend = "duckduckgo"
	}
	baseURL := cfg.BaseURL
	if overrideURL != "" {
		baseURL = overrideURL
	}
	return &WebSearchTool{
		backend: backend,
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (t *WebSearchTool) Name() string        { return "web_search" }
func (t *WebSearchTool) Description() string { return "Search the web for information." }

func (t *WebSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query"},
			"num_results": {"type": "integer", "description": "Number of results (default 5, max 10)"}
		},
		"required": ["query"]
	}`)
}

func (t *WebSearchTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Query      string `json:"query"`
		NumResults int    `json:"num_results"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Query == "" {
		return agent.ToolResult{Content: "query is required", IsError: true}, nil
	}
	if p.NumResults <= 0 {
		p.NumResults = 5
	}
	if p.NumResults > 10 {
		p.NumResults = 10
	}

	switch t.backend {
	case "brave":
		return t.searchBrave(ctx, p.Query, p.NumResults)
	case "searxng":
		return t.searchSearXNG(ctx, p.Query, p.NumResults)
	default:
		return t.searchDuckDuckGo(ctx, p.Query, p.NumResults)
	}
}

func (t *WebSearchTool) searchDuckDuckGo(ctx context.Context, query string, n int) (agent.ToolResult, error) {
	base := "https://api.duckduckgo.com"
	if t.baseURL != "" {
		base = t.baseURL
	}
	endpoint := base + "/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("request error: %v", err), IsError: true}, nil
	}
	req.Header.Set("User-Agent", "capabot/1.0")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("search error: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return agent.ToolResult{Content: fmt.Sprintf("search returned HTTP %d", resp.StatusCode), IsError: true}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	var ddgResp struct {
		AbstractText  string `json:"AbstractText"`
		RelatedTopics []struct {
			Text string `json:"Text"`
		} `json:"RelatedTopics"`
	}
	if err := json.Unmarshal(body, &ddgResp); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	var lines []string
	if ddgResp.AbstractText != "" {
		lines = append(lines, "• "+ddgResp.AbstractText)
	}
	for _, topic := range ddgResp.RelatedTopics {
		if topic.Text != "" && len(lines) < n {
			lines = append(lines, "• "+topic.Text)
		}
	}

	if len(lines) == 0 {
		return agent.ToolResult{Content: "No results found."}, nil
	}
	return agent.ToolResult{Content: strings.Join(lines, "\n")}, nil
}

func (t *WebSearchTool) searchBrave(ctx context.Context, query string, n int) (agent.ToolResult, error) {
	endpoint := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), n)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("request error: %v", err), IsError: true}, nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("search error: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return agent.ToolResult{Content: fmt.Sprintf("search returned HTTP %d", resp.StatusCode), IsError: true}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &braveResp); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	var lines []string
	for _, r := range braveResp.Web.Results {
		if len(lines) >= n {
			break
		}
		line := fmt.Sprintf("• %s\n  %s\n  %s", r.Title, r.URL, r.Description)
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return agent.ToolResult{Content: "No results found."}, nil
	}
	return agent.ToolResult{Content: strings.Join(lines, "\n\n")}, nil
}

func (t *WebSearchTool) searchSearXNG(ctx context.Context, query string, n int) (agent.ToolResult, error) {
	baseURL := t.baseURL
	if baseURL == "" {
		return agent.ToolResult{Content: "searxng base_url not configured", IsError: true}, nil
	}
	endpoint := fmt.Sprintf("%s/search?q=%s&format=json", strings.TrimRight(baseURL, "/"), url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("request error: %v", err), IsError: true}, nil
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("search error: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return agent.ToolResult{Content: fmt.Sprintf("search returned HTTP %d", resp.StatusCode), IsError: true}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	var searxResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &searxResp); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("parse error: %v", err), IsError: true}, nil
	}

	var lines []string
	for _, r := range searxResp.Results {
		if len(lines) >= n {
			break
		}
		line := fmt.Sprintf("• %s\n  %s\n  %s", r.Title, r.URL, r.Content)
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return agent.ToolResult{Content: "No results found."}, nil
	}
	return agent.ToolResult{Content: strings.Join(lines, "\n\n")}, nil
}
