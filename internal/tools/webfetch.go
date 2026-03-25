package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/polymath/gostaff/internal/agent"
)

// WebFetchTool implements the web_fetch tool.
type WebFetchTool struct {
	httpClient *http.Client
	maxBytes   int
}

// NewWebFetchTool creates a web_fetch tool with a 512KB limit.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		maxBytes:   512 * 1024,
	}
}

func (t *WebFetchTool) Name() string        { return "web_fetch" }
func (t *WebFetchTool) Description() string { return "Fetch content from a URL." }

func (t *WebFetchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "URL to fetch"},
			"extract_text": {"type": "boolean", "description": "Strip HTML tags and return plain text (default true)"}
		},
		"required": ["url"]
	}`)
}

var (
	reScript     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle      = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reHead       = regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	reTag        = regexp.MustCompile(`<[^>]+>`)
	reWhitespace = regexp.MustCompile(`\s{2,}`)
)

func (t *WebFetchTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		URL         string `json:"url"`
		ExtractText *bool  `json:"extract_text"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}

	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return agent.ToolResult{Content: "invalid URL: must be http or https", IsError: true}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("request error: %v", err), IsError: true}, nil
	}
	req.Header.Set("User-Agent", "gostaff/1.0")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("fetch error: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return agent.ToolResult{Content: fmt.Sprintf("HTTP %d", resp.StatusCode), IsError: true}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.maxBytes)))
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	content := string(body)

	// Default extract_text to true
	extractText := p.ExtractText == nil || *p.ExtractText
	if extractText {
		content = stripHTML(content)
	}

	return agent.ToolResult{Content: content}, nil
}

func stripHTML(html string) string {
	s := reHead.ReplaceAllString(html, " ")
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, " ")
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
