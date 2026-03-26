package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/api"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/rs/zerolog"
)

func newTestServer(t *testing.T, cfg api.Config) *httptest.Server {
	t.Helper()
	cfg.Logger = zerolog.Nop()
	srv := api.New(cfg)
	return httptest.NewServer(srv.Handler())
}

func TestHealthEndpoint(t *testing.T) {
	ts := newTestServer(t, api.Config{})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("want status=ok, got %v", body["status"])
	}
}

func TestChatEndpoint(t *testing.T) {
	agentCalled := false
	ts := newTestServer(t, api.Config{
		RunAgent: func(_ context.Context, _, _, _ string, msgs []llm.ChatMessage, _ func(agent.AgentEvent)) (*agent.RunResult, error) {
			agentCalled = true
			return &agent.RunResult{Response: "pong"}, nil
		},
	})
	defer ts.Close()

	body := `{"text":"ping","session_id":"test-1"}`
	resp, err := http.Post(ts.URL+"/api/chat", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if !agentCalled {
		t.Fatal("agent was not called")
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["response"] != "pong" {
		t.Fatalf("want response=pong, got %v", result["response"])
	}
}

func TestChatEndpointMissingText(t *testing.T) {
	ts := newTestServer(t, api.Config{})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/chat", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestAgentsEndpointNoRegistry(t *testing.T) {
	ts := newTestServer(t, api.Config{})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var result []any
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 0 {
		t.Fatalf("want empty list, got %v", result)
	}
}

func TestProvidersEndpoint(t *testing.T) {
	ts := newTestServer(t, api.Config{
		Providers: map[string]llm.Provider{},
	})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware(t *testing.T) {
	ts := newTestServer(t, api.Config{
		APIKey: "secret-token",
	})
	defer ts.Close()

	// Without auth — should fail
	resp, err := http.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without auth, got %d", resp.StatusCode)
	}

	// With correct token — should pass
	req, _ := http.NewRequest("GET", ts.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("want 200 with valid auth, got %d", resp2.StatusCode)
	}

	// Health endpoint bypasses auth
	resp3, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("want health to bypass auth, got %d", resp3.StatusCode)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	ts := newTestServer(t, api.Config{
		RateLimitRPM: 2, // token bucket starts full with 2 tokens
	})
	defer ts.Close()

	// First two requests succeed (consume both tokens)
	for i := 0; i < 2; i++ {
		resp, err := http.Get(ts.URL + "/api/health")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i+1, resp.StatusCode)
		}
	}

	// Third request should be rate-limited (no tokens left)
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("want 429 after limit exceeded, got %d", resp.StatusCode)
	}
}
