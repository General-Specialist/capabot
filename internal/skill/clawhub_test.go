package skill_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polymath/capabot/internal/skill"
)

// mockClawHubServer returns a test server that mimics the GitHub Contents API
// and raw.githubusercontent.com for a tiny two-skill catalog.
// All URLs (API + raw) are served from this single server so the rewriteTransport
// can redirect both api.github.com and raw.githubusercontent.com here.
func mockClawHubServer(t *testing.T) *httptest.Server {
	t.Helper()

	var ts *httptest.Server
	mux := http.NewServeMux()

	// GET /repos/openclaw/skills/contents/skills — directory listing
	mux.HandleFunc("/repos/openclaw/skills/contents/skills", func(w http.ResponseWriter, r *http.Request) {
		entries := []map[string]any{
			{"name": "echo-skill", "path": "skills/echo-skill", "type": "dir", "html_url": "https://github.com/openclaw/skills/tree/main/skills/echo-skill"},
			{"name": "grep-skill", "path": "skills/grep-skill", "type": "dir", "html_url": "https://github.com/openclaw/skills/tree/main/skills/grep-skill"},
			{"name": "README.md", "path": "skills/README.md", "type": "file"}, // non-dir, should be ignored
		}
		json.NewEncoder(w).Encode(entries)
	})

	// GET /repos/openclaw/skills/contents/skills/echo-skill — file listing for DownloadSkill
	mux.HandleFunc("/repos/openclaw/skills/contents/skills/echo-skill", func(w http.ResponseWriter, r *http.Request) {
		// download_url must be a URL that our rewriteTransport can reach
		dlURL := ts.URL + "/openclaw/skills/main/skills/echo-skill/SKILL.md"
		entries := []map[string]any{
			{
				"name":         "SKILL.md",
				"path":         "skills/echo-skill/SKILL.md",
				"type":         "file",
				"download_url": dlURL,
			},
		}
		json.NewEncoder(w).Encode(entries)
	})

	// Raw SKILL.md content — served at the path fetchParsedSkill constructs:
	// /openclaw/skills/main/skills/{name}/SKILL.md
	mux.HandleFunc("/openclaw/skills/main/skills/echo-skill/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\nname: echo-skill\ndescription: Echoes input back\nversion: \"1.0.0\"\n---\nEcho whatever is given.\n"))
	})
	mux.HandleFunc("/openclaw/skills/main/skills/grep-skill/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\nname: grep-skill\ndescription: Search file contents\nversion: \"2.0.0\"\n---\nSearch for patterns.\n"))
	})

	ts = httptest.NewServer(mux)
	return ts
}

// newTestClawHubClient returns a ClawHubClient pointed at the mock server.
// It overrides the raw.githubusercontent.com host by injecting a custom
// HTTPClient that rewrites raw URLs to the test server.
func newTestClawHubClient(ts *httptest.Server) *skill.ClawHubClient {
	return skill.NewClawHubClient(skill.ClawHubConfig{
		Owner:  "openclaw",
		Repo:   "skills",
		Branch: "main",
		HTTPClient: &http.Client{
			Transport: &rewriteTransport{base: ts.URL},
		},
	})
}

// rewriteTransport rewrites all request URLs to the test server base.
type rewriteTransport struct{ base string }

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	// Replace host (api.github.com or raw.githubusercontent.com) with test server
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.base, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

func TestClawHubClient_ListSkills(t *testing.T) {
	ts := mockClawHubServer(t)
	defer ts.Close()

	client := newTestClawHubClient(ts)
	skills, err := client.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("want 2 skills, got %d", len(skills))
	}
	// Metadata should be extracted from SKILL.md
	for _, s := range skills {
		if s.Description == "" {
			t.Errorf("skill %q has empty description", s.Name)
		}
	}
}

func TestClawHubClient_SearchSkills(t *testing.T) {
	ts := mockClawHubServer(t)
	defer ts.Close()

	client := newTestClawHubClient(ts)

	results, err := client.SearchSkills(context.Background(), "echo")
	if err != nil {
		t.Fatalf("SearchSkills error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result for 'echo', got %d", len(results))
	}
	if results[0].Name != "echo-skill" {
		t.Errorf("want echo-skill, got %q", results[0].Name)
	}
}

func TestClawHubClient_SearchSkills_NoMatch(t *testing.T) {
	ts := mockClawHubServer(t)
	defer ts.Close()

	client := newTestClawHubClient(ts)
	results, err := client.SearchSkills(context.Background(), "zzz-nonexistent")
	if err != nil {
		t.Fatalf("SearchSkills error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestClawHubClient_DownloadSkill(t *testing.T) {
	ts := mockClawHubServer(t)
	defer ts.Close()

	// Update mock to serve echo-skill files with absolute download_url
	// (the rewriteTransport will redirect to the test server)
	destDir := t.TempDir()
	client := newTestClawHubClient(ts)

	skillPath, err := client.DownloadSkill(context.Background(), "echo-skill", destDir)
	if err != nil {
		t.Fatalf("DownloadSkill error: %v", err)
	}
	if skillPath == "" {
		t.Fatal("expected non-empty skill path")
	}

	// SKILL.md should exist in the downloaded directory
	skillMD := filepath.Join(skillPath, "SKILL.md")
	if _, err := os.Stat(skillMD); os.IsNotExist(err) {
		t.Errorf("SKILL.md not found at %s", skillMD)
	}
}

func TestClawHubClient_DownloadSkill_InvalidName(t *testing.T) {
	ts := mockClawHubServer(t)
	defer ts.Close()

	client := newTestClawHubClient(ts)
	_, err := client.DownloadSkill(context.Background(), "../etc/passwd", t.TempDir())
	if err == nil {
		t.Fatal("expected error for path-traversal name")
	}
}
