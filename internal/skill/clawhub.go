package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	clawHubBase   = "https://clawhub.ai"
	clawHubConvex = "https://wry-manatee-359.convex.cloud"
)

// ClawHubSkillEntry is a skill listed in the ClawHub catalog.
type ClawHubSkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Path        string `json:"path"`
	HTMLURL     string `json:"html_url"`
	Downloads   int64  `json:"downloads"`
	Stars       int64  `json:"stars"`
}

// ClawHubConfig controls the registry client.
type ClawHubConfig struct {
	// Token is an optional auth token (currently unused for public catalog).
	Token string
	// HTTPClient allows injecting a custom http.Client (nil = default).
	HTTPClient *http.Client
}

// ClawHubClient fetches skills from the real ClawHub API.
type ClawHubClient struct {
	token      string
	httpClient *http.Client
}

// NewClawHubClient creates a client with the given config.
func NewClawHubClient(cfg ClawHubConfig) *ClawHubClient {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &ClawHubClient{token: cfg.Token, httpClient: hc}
}

// convexPageItem is one item from the listPublicPageV4 Convex query.
type convexPageItem struct {
	Skill struct {
		Slug        string  `json:"slug"`
		DisplayName string  `json:"displayName"`
		Summary     string  `json:"summary"`
		UpdatedAt   float64 `json:"updatedAt"`
		Stats       struct {
			Downloads float64 `json:"downloads"`
			Stars     float64 `json:"stars"`
		} `json:"stats"`
	} `json:"skill"`
	LatestVersion *struct {
		Version string `json:"version"`
	} `json:"latestVersion"`
}

// BrowseSkills returns skills from the ClawHub catalog (top by downloads).
// If query is non-empty, delegates to vector search instead.
func (c *ClawHubClient) BrowseSkills(ctx context.Context, query string, limit, offset int) ([]ClawHubSkillEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	if query != "" {
		return c.SearchSkills(ctx, query)
	}

	const batchSize = 100
	var (
		entries []ClawHubSkillEntry
		cursor  *string
	)

	for len(entries) < limit+offset {
		args := map[string]any{
			"numItems":          batchSize,
			"sort":              "downloads",
			"dir":               "desc",
			"highlightedOnly":   false,
			"nonSuspiciousOnly": false,
		}
		if cursor != nil {
			args["cursor"] = *cursor
		}

		body, _ := json.Marshal(map[string]any{"path": "skills:listPublicPageV4", "args": args})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, clawHubConvex+"/api/query", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching ClawHub catalog: %w", err)
		}

		var result struct {
			Status string `json:"status"`
			Value  struct {
				Page       []convexPageItem `json:"page"`
				HasMore    bool             `json:"hasMore"`
				NextCursor *string          `json:"nextCursor"`
			} `json:"value"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("decoding ClawHub response: %w", decErr)
		}
		if resp.StatusCode != http.StatusOK || result.Status != "success" {
			return nil, fmt.Errorf("ClawHub API error: HTTP %d, status %s", resp.StatusCode, result.Status)
		}

		for _, item := range result.Value.Page {
			version := ""
			if item.LatestVersion != nil {
				version = item.LatestVersion.Version
			}
			entries = append(entries, ClawHubSkillEntry{
				Name:        item.Skill.DisplayName,
				Description: item.Skill.Summary,
				Version:     version,
				Path:        item.Skill.Slug,
				HTMLURL:     clawHubBase + "/skills/" + item.Skill.Slug,
				Downloads:   int64(item.Skill.Stats.Downloads),
				Stars:       int64(item.Skill.Stats.Stars),
			})
		}

		if !result.Value.HasMore || result.Value.NextCursor == nil {
			break
		}
		cursor = result.Value.NextCursor
	}

	if offset >= len(entries) {
		return []ClawHubSkillEntry{}, nil
	}
	entries = entries[offset:]
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// searchResult is the shape returned by /api/v1/search.
type searchResult struct {
	Results []struct {
		Slug        string  `json:"slug"`
		DisplayName string  `json:"displayName"`
		Summary     string  `json:"summary"`
		Score       float64 `json:"score"`
	} `json:"results"`
}

// SearchSkills searches ClawHub using vector search.
func (c *ClawHubClient) SearchSkills(ctx context.Context, query string) ([]ClawHubSkillEntry, error) {
	u := clawHubBase + "/api/v1/search?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching ClawHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ClawHub search error: HTTP %d", resp.StatusCode)
	}

	var result searchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	entries := make([]ClawHubSkillEntry, len(result.Results))
	for i, r := range result.Results {
		entries[i] = ClawHubSkillEntry{
			Name:        r.DisplayName,
			Description: r.Summary,
			Path:        r.Slug,
			HTMLURL:     clawHubBase + "/skills/" + r.Slug,
		}
	}
	return entries, nil
}

// DownloadSkill downloads a skill ZIP by slug and extracts it under destDir.
// Returns the path to the extracted skill directory.
func (c *ClawHubClient) DownloadSkill(ctx context.Context, slug, destDir string) (string, error) {
	if strings.ContainsAny(slug, "/\\.") {
		return "", fmt.Errorf("invalid skill slug %q", slug)
	}

	u := clawHubBase + "/api/v1/download?slug=" + url.QueryEscape(slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading skill %q: %w", slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ClawHub download error: HTTP %d for %q", resp.StatusCode, slug)
	}

	zipData, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024)) // 32 MiB max
	if err != nil {
		return "", fmt.Errorf("reading skill zip: %w", err)
	}

	skillDir := filepath.Join(destDir, slug)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", fmt.Errorf("creating skill directory: %w", err)
	}

	if err := extractZip(zipData, skillDir); err != nil {
		return "", fmt.Errorf("extracting skill zip: %w", err)
	}

	return skillDir, nil
}

func extractZip(data []byte, destDir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range r.File {
		// Sanitize path to prevent directory traversal
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") {
			continue
		}
		destPath := filepath.Join(destDir, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, 4*1024*1024))
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
