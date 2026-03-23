package skill

import (
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
	// ClawHubDefaultOwner is the GitHub organisation hosting the skill catalog.
	ClawHubDefaultOwner = "openclaw"
	// ClawHubDefaultRepo is the GitHub repository name.
	ClawHubDefaultRepo = "skills"
	// ClawHubDefaultBranch is the branch to pull from.
	ClawHubDefaultBranch = "main"
)

// ClawHubSkillEntry is a skill listed in the ClawHub directory.
type ClawHubSkillEntry struct {
	// Name is the skill directory name (also used as the skill identifier).
	Name string `json:"name"`
	// Description is extracted from the skill's SKILL.md frontmatter.
	Description string `json:"description"`
	// Version is the skill version string, if declared.
	Version string `json:"version"`
	// Path is the relative path in the repo (e.g., "skills/my-skill").
	Path string `json:"path"`
	// HTMLURL is the link to the GitHub directory.
	HTMLURL string `json:"html_url"`
}

// ClawHubConfig controls the registry client.
type ClawHubConfig struct {
	// Owner is the GitHub org/user owning the skills repo (default: openclaw).
	Owner string
	// Repo is the GitHub repository name (default: skills).
	Repo string
	// Branch is the branch to pull from (default: main).
	Branch string
	// GitHubToken is an optional personal access token to increase rate limits.
	GitHubToken string
	// HTTPClient allows injecting a custom http.Client (nil = default).
	HTTPClient *http.Client
}

// ClawHubClient fetches skills from the ClawHub GitHub repository.
type ClawHubClient struct {
	owner      string
	repo       string
	branch     string
	token      string
	httpClient *http.Client
}

// NewClawHubClient creates a client with the given config. Zero-value fields
// fall back to the ClawHub defaults.
func NewClawHubClient(cfg ClawHubConfig) *ClawHubClient {
	if cfg.Owner == "" {
		cfg.Owner = ClawHubDefaultOwner
	}
	if cfg.Repo == "" {
		cfg.Repo = ClawHubDefaultRepo
	}
	if cfg.Branch == "" {
		cfg.Branch = ClawHubDefaultBranch
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &ClawHubClient{
		owner:      cfg.Owner,
		repo:       cfg.Repo,
		branch:     cfg.Branch,
		token:      cfg.GitHubToken,
		httpClient: hc,
	}
}

// ListSkills fetches all skill entries from the ClawHub directory.
// It uses the GitHub Contents API to list the top-level `skills/` directory,
// then fetches each skill's SKILL.md to extract name/description/version.
func (c *ClawHubClient) ListSkills(ctx context.Context) ([]ClawHubSkillEntry, error) {
	// Step 1: list the skills/ directory via GitHub API
	apiURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/contents/skills?ref=%s",
		c.owner, c.repo, c.branch,
	)

	var dirs []githubContentEntry
	if err := c.getJSON(ctx, apiURL, &dirs); err != nil {
		return nil, fmt.Errorf("listing ClawHub skills directory: %w", err)
	}

	entries := make([]ClawHubSkillEntry, 0, len(dirs))
	for _, d := range dirs {
		if d.Type != "dir" {
			continue
		}
		entry := ClawHubSkillEntry{
			Name:    d.Name,
			Path:    d.Path,
			HTMLURL: d.HTMLURL,
		}
		// Fetch the SKILL.md to extract metadata (best-effort; skip on error)
		parsed, err := c.fetchParsedSkill(ctx, d.Name)
		if err == nil && parsed != nil {
			if parsed.Manifest.Name != "" {
				entry.Name = parsed.Manifest.Name
			}
			entry.Description = parsed.Manifest.Description
			entry.Version = parsed.Manifest.Version
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// SearchSkills returns all skills whose name or description contains query
// (case-insensitive). It fetches the full listing on first call.
func (c *ClawHubClient) SearchSkills(ctx context.Context, query string) ([]ClawHubSkillEntry, error) {
	all, err := c.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	results := make([]ClawHubSkillEntry, 0)
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Description), q) {
			results = append(results, e)
		}
	}
	return results, nil
}

// DownloadSkill downloads all files for the named skill directory and writes
// them under destDir/<skillDirName>/. Returns the path to the downloaded
// skill directory.
//
// The skill can then be imported with ImportSkill(destPath, registryDir).
func (c *ClawHubClient) DownloadSkill(ctx context.Context, skillDirName, destDir string) (string, error) {
	// Validate name: no path traversal
	if strings.ContainsAny(skillDirName, "/\\..") {
		return "", fmt.Errorf("invalid skill name %q", skillDirName)
	}

	apiURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/contents/skills/%s?ref=%s",
		c.owner, c.repo, url.PathEscape(skillDirName), c.branch,
	)

	var files []githubContentEntry
	if err := c.getJSON(ctx, apiURL, &files); err != nil {
		return "", fmt.Errorf("fetching skill %q from ClawHub: %w", skillDirName, err)
	}

	skillPath := filepath.Join(destDir, skillDirName)
	if err := os.MkdirAll(skillPath, 0o755); err != nil {
		return "", fmt.Errorf("creating skill directory: %w", err)
	}

	for _, f := range files {
		if f.Type != "file" {
			continue // skip nested dirs (rare in skill repos)
		}
		if err := c.downloadFile(ctx, f.DownloadURL, filepath.Join(skillPath, f.Name)); err != nil {
			return "", fmt.Errorf("downloading %s: %w", f.Name, err)
		}
	}

	return skillPath, nil
}

// fetchParsedSkill downloads and parses a skill's SKILL.md. Returns nil on any
// error so callers can treat metadata as best-effort.
func (c *ClawHubClient) fetchParsedSkill(ctx context.Context, skillDirName string) (*ParsedSkill, error) {
	rawURL := fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/%s/skills/%s/SKILL.md",
		c.owner, c.repo, c.branch, url.PathEscape(skillDirName),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	return ParseSkillMD(body)
}

func (c *ClawHubClient) downloadFile(ctx context.Context, rawURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, rawURL)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, io.LimitReader(resp.Body, 4*1024*1024)) // 4 MiB max per file
	return err
}

func (c *ClawHubClient) getJSON(ctx context.Context, apiURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("GitHub API rate limit hit (status %d) — set CAPABOT_GITHUB_TOKEN to increase limits", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API error: HTTP %d for %s", resp.StatusCode, apiURL)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *ClawHubClient) setHeaders(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// githubContentEntry is the GitHub Contents API response shape.
type githubContentEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "file" or "dir"
	DownloadURL string `json:"download_url"`
	HTMLURL     string `json:"html_url"`
}
