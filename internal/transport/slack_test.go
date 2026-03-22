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

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

var slackWsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// newTestSlack returns a SlackTransport wired to test servers.
// connectionsOpenURL is overridden to point to the provided httptest.Server.
// postMessageURL is similarly overridable.
func newTestSlack(connectionsOpenURL, postMessageURL string) *SlackTransport {
	logger := zerolog.Nop()
	t := NewSlackTransport(SlackConfig{
		AppToken: "xapp-test",
		BotToken: "xoxb-test",
	}, logger)
	if connectionsOpenURL != "" {
		t.connectionsOpenURL = connectionsOpenURL
	}
	if postMessageURL != "" {
		t.postMessageURL = postMessageURL
	}
	return t
}

// ---------------------------------------------------------------------------
// Test 1: Name() returns "slack"
// ---------------------------------------------------------------------------

func TestSlackName(t *testing.T) {
	s := newTestSlack("", "")
	if got := s.Name(); got != "slack" {
		t.Errorf("Name() = %q; want %q", got, "slack")
	}
}

// ---------------------------------------------------------------------------
// Test 2: Send posts to the correct endpoint with Bearer token
// ---------------------------------------------------------------------------

func TestSlackSendPostsToCorrectEndpoint(t *testing.T) {
	var (
		gotAuth    string
		gotBody    map[string]string
		gotPath    string
		mu         sync.Mutex
		requestsCh = make(chan struct{}, 1)
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
		requestsCh <- struct{}{}
	}))
	defer srv.Close()

	s := newTestSlack("", srv.URL)

	ctx := context.Background()
	err := s.Send(ctx, OutboundMessage{
		ChannelID: "C123",
		Text:      "hello world",
	})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	select {
	case <-requestsCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request")
	}

	mu.Lock()
	defer mu.Unlock()

	if gotAuth != "Bearer xoxb-test" {
		t.Errorf("Authorization header = %q; want %q", gotAuth, "Bearer xoxb-test")
	}
	if gotPath != "/" {
		// httptest server root; the full URL replaces postMessageURL, so path is /
		// (no path suffix expected — we override the full URL)
	}
	if gotBody["channel"] != "C123" {
		t.Errorf("body channel = %q; want %q", gotBody["channel"], "C123")
	}
	if gotBody["text"] != "hello world" {
		t.Errorf("body text = %q; want %q", gotBody["text"], "hello world")
	}
}

// ---------------------------------------------------------------------------
// Test 3: Long message (>3000 chars) splits into multiple posts
// ---------------------------------------------------------------------------

func TestSlackSendLongMessageSplits(t *testing.T) {
	var (
		mu       sync.Mutex
		received []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		received = append(received, body["text"])
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	s := newTestSlack("", srv.URL)

	// Build a message that is 3001 runes long (just over the 3000-char limit).
	longText := strings.Repeat("a", 3001)
	ctx := context.Background()
	if err := s.Send(ctx, OutboundMessage{ChannelID: "C1", Text: longText}); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Errorf("got %d chunks; want 2", len(received))
	}
	if len(received) >= 1 && len([]rune(received[0])) != 3000 {
		t.Errorf("first chunk length = %d; want 3000", len([]rune(received[0])))
	}
	if len(received) >= 2 && len([]rune(received[1])) != 1 {
		t.Errorf("second chunk length = %d; want 1", len([]rune(received[1])))
	}
}

// ---------------------------------------------------------------------------
// Test 4: Incoming message dispatched to handler with correct fields
// ---------------------------------------------------------------------------

func TestSlackIncomingMessageDispatchedToHandler(t *testing.T) {
	// We need a mock WebSocket server plus a connections.open endpoint.

	// Channel for received inbound messages.
	msgCh := make(chan InboundMessage, 1)

	// The WebSocket server sends one events_api envelope and then waits.
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := slackWsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send hello first.
		hello, _ := json.Marshal(map[string]string{"type": "hello"})
		conn.WriteMessage(websocket.TextMessage, hello)

		// Send a message event.
		env := map[string]any{
			"type":        "events_api",
			"envelope_id": "env-001",
			"payload": map[string]any{
				"event": map[string]any{
					"type":     "message",
					"text":     "ping",
					"channel":  "C999",
					"user":     "U42",
					"event_ts": "ts-1",
				},
			},
		}
		data, _ := json.Marshal(env)
		conn.WriteMessage(websocket.TextMessage, data)

		// Wait for ack then keep connection open until client closes.
		conn.ReadMessage() // consume ack
		// Block until the test is done.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer wsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")

	// connections.open mock returns the WebSocket URL.
	openSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":  true,
			"url": wsURL,
		})
	}))
	defer openSrv.Close()

	s := newTestSlack(openSrv.URL, "")
	s.OnMessage(func(_ context.Context, msg InboundMessage) {
		msgCh <- msg
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.Start(ctx) //nolint:errcheck

	select {
	case msg := <-msgCh:
		if msg.Text != "ping" {
			t.Errorf("Text = %q; want %q", msg.Text, "ping")
		}
		if msg.ChannelID != "C999" {
			t.Errorf("ChannelID = %q; want %q", msg.ChannelID, "C999")
		}
		if msg.UserID != "U42" {
			t.Errorf("UserID = %q; want %q", msg.UserID, "U42")
		}
		if msg.Platform != "slack" {
			t.Errorf("Platform = %q; want %q", msg.Platform, "slack")
		}
		if msg.ID != "ts-1" {
			t.Errorf("ID = %q; want %q", msg.ID, "ts-1")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for inbound message")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Envelope acknowledgement sent on message receipt
// ---------------------------------------------------------------------------

func TestSlackEnvelopeAcknowledgementSent(t *testing.T) {
	ackCh := make(chan map[string]any, 1)

	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := slackWsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a hello.
		hello, _ := json.Marshal(map[string]string{"type": "hello"})
		conn.WriteMessage(websocket.TextMessage, hello)

		// Send a message event with a specific envelope_id.
		env := map[string]any{
			"type":        "events_api",
			"envelope_id": "ack-envelope-42",
			"payload": map[string]any{
				"event": map[string]any{
					"type":    "message",
					"text":    "test",
					"channel": "C1",
					"user":    "U1",
				},
			},
		}
		data, _ := json.Marshal(env)
		conn.WriteMessage(websocket.TextMessage, data)

		// Read the acknowledgement that the transport should send back.
		_, ackData, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var ack map[string]any
		if jsonErr := json.Unmarshal(ackData, &ack); jsonErr == nil {
			ackCh <- ack
		}

		// Keep connection open.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer wsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")

	openSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":  true,
			"url": wsURL,
		})
	}))
	defer openSrv.Close()

	s := newTestSlack(openSrv.URL, "")
	s.OnMessage(func(_ context.Context, _ InboundMessage) {}) // no-op handler

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.Start(ctx) //nolint:errcheck

	select {
	case ack := <-ackCh:
		if ack["envelope_id"] != "ack-envelope-42" {
			t.Errorf("ack envelope_id = %v; want %q", ack["envelope_id"], "ack-envelope-42")
		}
		payload, ok := ack["payload"].(map[string]any)
		if !ok {
			t.Fatalf("ack payload is not a map: %T", ack["payload"])
		}
		if payload["text"] != "ok" {
			t.Errorf("ack payload text = %v; want %q", payload["text"], "ok")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for acknowledgement")
	}
}
