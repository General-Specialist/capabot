package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
)

// mockRESTServer builds an httptest.Server that handles /channels/{id}/messages.
// It records POSTed message bodies in `sent`.
func mockRESTServer(t *testing.T, sent *[]map[string]interface{}, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/channels/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		*sent = append(*sent, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"id": "resp-msg-id"}) //nolint:errcheck
	})
	return httptest.NewServer(mux)
}

// --- URL-rewriting HTTP client ---

// rewritingTransport replaces the scheme+host of every request with baseURL
// and strips the "/api/v10" prefix so that paths like /api/v10/channels/...
// are forwarded as /channels/... to the local test server.
type rewritingTransport struct {
	base    string
	wrapped http.RoundTripper
}

func (rt *rewritingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = strings.TrimPrefix(rt.base, "http://")
	cloned.URL.Path = strings.TrimPrefix(cloned.URL.Path, "/api/v10")
	return rt.wrapped.RoundTrip(cloned)
}

func newRewritingClient(baseURL string, inner *http.Client) *http.Client {
	return &http.Client{
		Transport: &rewritingTransport{base: baseURL, wrapped: inner.Transport},
		Timeout:   inner.Timeout,
	}
}

// --- tests ---

func TestDiscordTransport_Name(t *testing.T) {
	dt := NewDiscordTransport(DiscordConfig{Token: "t", AppID: "a"}, zerolog.Nop())
	if got := dt.Name(); got != "discord" {
		t.Errorf("Name() = %q, want %q", got, "discord")
	}
}

func TestDiscordTransport_Send_Short(t *testing.T) {
	var sent []map[string]interface{}
	var mu sync.Mutex
	restSrv := mockRESTServer(t, &sent, &mu)
	defer restSrv.Close()

	dt := NewDiscordTransport(DiscordConfig{Token: "test-token"}, zerolog.Nop())
	dt.httpClient = newRewritingClient(restSrv.URL, restSrv.Client())

	msg := OutboundMessage{
		ChannelID: "chan-123",
		Text:      "hello world",
	}
	if err := dt.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(sent))
	}
	if sent[0]["content"] != "hello world" {
		t.Errorf("content = %q, want %q", sent[0]["content"], "hello world")
	}
	if _, hasRef := sent[0]["message_reference"]; hasRef {
		t.Errorf("expected no message_reference for non-reply")
	}
}

func TestDiscordTransport_Send_Long(t *testing.T) {
	var sent []map[string]interface{}
	var mu sync.Mutex
	restSrv := mockRESTServer(t, &sent, &mu)
	defer restSrv.Close()

	dt := NewDiscordTransport(DiscordConfig{Token: "test-token"}, zerolog.Nop())
	dt.httpClient = newRewritingClient(restSrv.URL, restSrv.Client())

	longText := strings.Repeat("a", 2001)
	msg := OutboundMessage{
		ChannelID: "chan-123",
		Text:      longText,
	}
	if err := dt.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) < 2 {
		t.Fatalf("expected ≥2 POSTs for long message, got %d", len(sent))
	}
	for _, chunk := range sent {
		content, _ := chunk["content"].(string)
		if len([]rune(content)) > discordMaxMsgLen {
			t.Errorf("chunk length %d exceeds limit %d", len([]rune(content)), discordMaxMsgLen)
		}
	}
}

func TestDiscordTransport_Send_WithReply(t *testing.T) {
	var sent []map[string]interface{}
	var mu sync.Mutex
	restSrv := mockRESTServer(t, &sent, &mu)
	defer restSrv.Close()

	dt := NewDiscordTransport(DiscordConfig{Token: "test-token"}, zerolog.Nop())
	dt.httpClient = newRewritingClient(restSrv.URL, restSrv.Client())

	msg := OutboundMessage{
		ChannelID: "chan-456",
		ReplyToID: "msg-789",
		Text:      "this is a reply",
	}
	if err := dt.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(sent))
	}
	ref, ok := sent[0]["message_reference"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected message_reference field, got %v", sent[0]["message_reference"])
	}
	if ref["message_id"] != "msg-789" {
		t.Errorf("message_reference.message_id = %q, want %q", ref["message_id"], "msg-789")
	}
}
