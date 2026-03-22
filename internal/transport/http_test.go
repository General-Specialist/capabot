package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/polymath/capabot/internal/transport"
)

func newTestTransport(t *testing.T, apiKeys []string) *transport.HTTPTransport {
	t.Helper()
	return transport.NewHTTPTransport(transport.HTTPConfig{
		Addr:    ":0",
		APIKeys: apiKeys,
	}, zerolog.Nop())
}

func TestHTTPTransport_Healthz(t *testing.T) {
	tr := newTestTransport(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestHTTPTransport_Chat_Success(t *testing.T) {
	tr := newTestTransport(t, nil)
	tr.OnMessage(func(ctx context.Context, msg transport.InboundMessage) {
		// Simulate agent responding
		time.Sleep(5 * time.Millisecond)
		tr.Send(ctx, transport.OutboundMessage{
			ChannelID: msg.ChannelID,
			Text:      "pong",
		})
	})

	body := `{"text":"ping","user_id":"u1"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["response"] != "pong" {
		t.Errorf("unexpected response: %v", resp)
	}
}

func TestHTTPTransport_Chat_Unauthorized(t *testing.T) {
	tr := newTestTransport(t, []string{"secret"})

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHTTPTransport_Chat_Authorized(t *testing.T) {
	tr := newTestTransport(t, []string{"secret"})
	tr.OnMessage(func(ctx context.Context, msg transport.InboundMessage) {
		tr.Send(ctx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: "ok"})
	})

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPTransport_Chat_EmptyText(t *testing.T) {
	tr := newTestTransport(t, nil)
	body := `{"text":""}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tr.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- helpers ---

// ServeHTTP exposes the internal mux for testing without starting a listener.
// We add this method via a thin test shim — but HTTPTransport doesn't expose
// its mux directly. Instead, use httptest.NewServer to test it properly.

// Actually, let's use httptest.NewServer for cleaner integration-style tests.

func TestHTTPTransport_ViaServer(t *testing.T) {
	tr := newTestTransport(t, nil)
	tr.OnMessage(func(ctx context.Context, msg transport.InboundMessage) {
		tr.Send(ctx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: "hello back"})
	})

	srv := httptest.NewServer(tr.Handler())
	defer srv.Close()

	// healthz
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: expected 200, got %d", resp.StatusCode)
	}

	// chat
	chatBody := `{"text":"hi"}`
	chatResp, err := http.Post(srv.URL+"/v1/chat", "application/json", strings.NewReader(chatBody))
	if err != nil {
		t.Fatal(err)
	}
	defer chatResp.Body.Close()
	if chatResp.StatusCode != http.StatusOK {
		t.Errorf("chat: expected 200, got %d", chatResp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(chatResp.Body).Decode(&result)
	if result["response"] != "hello back" {
		t.Errorf("unexpected response: %v", result)
	}
}
