package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// --- helpers ---

func newTestDiscord(t *testing.T, restServer *httptest.Server, gatewayURL string) *DiscordTransport {
	t.Helper()
	cfg := DiscordConfig{Token: "test-token", AppID: "test-app"}
	dt := NewDiscordTransport(cfg, zerolog.Nop())
	dt.httpClient = restServer.Client()
	dt.gatewayURLOverride = gatewayURL
	return dt
}

// mockRESTServer builds an httptest.Server that handles /gateway/bot and
// /channels/{id}/messages. It records POSTed message bodies in `sent`.
func mockRESTServer(t *testing.T, sent *[]map[string]interface{}, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/gateway/bot", func(w http.ResponseWriter, r *http.Request) {
		// Return a placeholder; real gateway URL set via override.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "wss://gateway.discord.gg"})
	})
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
		json.NewEncoder(w).Encode(map[string]string{"id": "resp-msg-id"})
	})
	return httptest.NewServer(mux)
}

// wsUpgrader is a permissive WebSocket upgrader for tests.
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// runGatewayServer starts an httptest.Server that acts as a minimal Discord
// gateway. It sends Hello, expects Identify, sends READY, then sends the
// provided extraEvents and closes.
func runGatewayServer(t *testing.T, extraEvents []gatewayPayload) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("ws upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// 1. Send OP 10 Hello
		hello := gatewayPayload{
			OP:   opHello,
			Data: mustMarshal(map[string]int{"heartbeat_interval": 100}),
		}
		if err := conn.WriteJSON(hello); err != nil {
			t.Logf("write hello error: %v", err)
			return
		}

		// 2. Expect OP 2 Identify (consume it; don't validate deeply in all tests)
		var identify gatewayPayload
		if err := conn.ReadJSON(&identify); err != nil {
			t.Logf("read identify error: %v", err)
			return
		}

		// 3. Send OP 0 READY
		seq := int64(1)
		ready := gatewayPayload{
			OP:        opDispatch,
			EventName: "READY",
			Sequence:  &seq,
			Data: mustMarshal(map[string]string{
				"session_id":          "sess-abc",
				"resume_gateway_url":  "wss://gateway.discord.gg",
			}),
		}
		if err := conn.WriteJSON(ready); err != nil {
			t.Logf("write ready error: %v", err)
			return
		}

		// 4. Send extra events
		for _, ev := range extraEvents {
			if err := conn.WriteJSON(ev); err != nil {
				t.Logf("write event error: %v", err)
				return
			}
		}

		// 5. Keep alive until the client closes (give it time to process).
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}))
	return srv
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
	dt.httpClient = restSrv.Client()
	// Point the REST base URL at our test server by replacing the httpClient
	// transport. We also need to override discordAPIBase per-call. Instead,
	// we do it by overriding the httpClient with one that rewrites URLs.
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

	// Build a message that is 2001 characters (just over the limit).
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

func TestDiscordTransport_GatewayHandshake(t *testing.T) {
	// Build a MESSAGE_CREATE event to dispatch after READY.
	msgSeq := int64(2)
	msgEvent := gatewayPayload{
		OP:        opDispatch,
		EventName: "MESSAGE_CREATE",
		Sequence:  &msgSeq,
		Data: mustMarshal(discordMessage{
			ID:        "msg-001",
			ChannelID: "chan-001",
			Content:   "hello from user",
			Author:    discordUser{ID: "user-001", Username: "Alice", Bot: false},
		}),
	}

	gwSrv := runGatewayServer(t, []gatewayPayload{msgEvent})
	defer gwSrv.Close()

	// The gateway server speaks ws://, so convert.
	gatewayWS := "ws" + strings.TrimPrefix(gwSrv.URL, "http")

	// REST server just needs to answer /gateway/bot (unused since we override).
	var noSent []map[string]interface{}
	var mu sync.Mutex
	restSrv := mockRESTServer(t, &noSent, &mu)
	defer restSrv.Close()

	dt := newTestDiscord(t, restSrv, gatewayWS)

	received := make(chan InboundMessage, 1)
	dt.OnMessage(func(ctx context.Context, msg InboundMessage) {
		received <- msg
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		_ = dt.Start(ctx)
	}()

	select {
	case msg := <-received:
		if msg.ID != "msg-001" {
			t.Errorf("msg.ID = %q, want %q", msg.ID, "msg-001")
		}
		if msg.Text != "hello from user" {
			t.Errorf("msg.Text = %q, want %q", msg.Text, "hello from user")
		}
		if msg.Username != "Alice" {
			t.Errorf("msg.Username = %q, want %q", msg.Username, "Alice")
		}
		if msg.Platform != "discord" {
			t.Errorf("msg.Platform = %q, want %q", msg.Platform, "discord")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for message handler to be called")
	}
}

func TestDiscordTransport_SkipsBotMessages(t *testing.T) {
	msgSeq := int64(2)
	botEvent := gatewayPayload{
		OP:        opDispatch,
		EventName: "MESSAGE_CREATE",
		Sequence:  &msgSeq,
		Data: mustMarshal(discordMessage{
			ID:        "bot-msg-001",
			ChannelID: "chan-001",
			Content:   "I am a bot",
			Author:    discordUser{ID: "bot-001", Username: "SomeBot", Bot: true},
		}),
	}

	gwSrv := runGatewayServer(t, []gatewayPayload{botEvent})
	defer gwSrv.Close()

	gatewayWS := "ws" + strings.TrimPrefix(gwSrv.URL, "http")

	var noSent []map[string]interface{}
	var mu sync.Mutex
	restSrv := mockRESTServer(t, &noSent, &mu)
	defer restSrv.Close()

	dt := newTestDiscord(t, restSrv, gatewayWS)

	handlerCalled := make(chan struct{}, 1)
	dt.OnMessage(func(ctx context.Context, msg InboundMessage) {
		handlerCalled <- struct{}{}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = dt.Start(ctx)
	}()

	select {
	case <-handlerCalled:
		t.Fatal("handler should NOT be called for bot messages")
	case <-ctx.Done():
		// Expected: timer expired without handler being invoked.
	}
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
	// Strip the production API path prefix so the test mux routes correctly.
	cloned.URL.Path = strings.TrimPrefix(cloned.URL.Path, "/api/v10")
	return rt.wrapped.RoundTrip(cloned)
}

func newRewritingClient(baseURL string, inner *http.Client) *http.Client {
	return &http.Client{
		Transport: &rewritingTransport{base: baseURL, wrapped: inner.Transport},
		Timeout:   inner.Timeout,
	}
}
