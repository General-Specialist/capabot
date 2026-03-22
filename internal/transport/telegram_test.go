package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// newTestTelegramTransport creates a TelegramTransport wired to the given mock server URL.
func newTestTelegramTransport(t *testing.T, serverURL string) *TelegramTransport {
	t.Helper()
	tr := NewTelegramTransport(TelegramConfig{Token: "testtoken"}, zerolog.Nop())
	// Override the base URL by patching the httpClient transport to rewrite requests.
	tr.httpClient = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &urlRewriter{base: serverURL, inner: http.DefaultTransport},
	}
	return tr
}

// urlRewriter is an http.RoundTripper that rewrites the host/scheme of every
// request to point at the mock server, preserving path and query.
type urlRewriter struct {
	base  string // e.g. "http://127.0.0.1:PORT"
	inner http.RoundTripper
}

func (u *urlRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we don't mutate the original.
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	// Strip the scheme+host from base and set on cloned request.
	trimmed := strings.TrimPrefix(u.base, "http://")
	trimmed = strings.TrimPrefix(trimmed, "https://")
	cloned.URL.Host = trimmed
	return u.inner.RoundTrip(cloned)
}

// writeTgResponse writes a standard Telegram API response envelope.
func writeTgResponse(w http.ResponseWriter, result any) {
	raw, _ := json.Marshal(result)
	resp := map[string]any{"ok": true, "result": json.RawMessage(raw)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// 1. Name
// ---------------------------------------------------------------------------

func TestTelegramTransport_Name(t *testing.T) {
	tr := NewTelegramTransport(TelegramConfig{Token: "x"}, zerolog.Nop())
	if got := tr.Name(); got != "telegram" {
		t.Fatalf("expected 'telegram', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// 2. Long-poll receives a message
// ---------------------------------------------------------------------------

func TestTelegramTransport_LongPoll_ReceivesMessage(t *testing.T) {
	var callCount int32

	// First call returns one update; subsequent calls return empty (and block
	// via context cancellation shortly after).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			updates := []tgUpdate{
				{
					UpdateID: 100,
					Message: &tgMessage{
						MessageID: 1,
						From:      tgUser{ID: 42, Username: "alice"},
						Chat:      tgChat{ID: 99, Type: "private"},
						Text:      "hello",
					},
				},
			}
			writeTgResponse(w, updates)
			return
		}
		// Block until the client disconnects (simulates long-poll wait).
		<-r.Context().Done()
		writeTgResponse(w, []tgUpdate{})
	}))
	defer srv.Close()

	tr := newTestTelegramTransport(t, srv.URL)

	received := make(chan InboundMessage, 1)
	tr.OnMessage(func(_ context.Context, msg InboundMessage) {
		received <- msg
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go tr.Start(ctx) //nolint:errcheck

	select {
	case msg := <-received:
		if msg.Text != "hello" {
			t.Errorf("expected text 'hello', got %q", msg.Text)
		}
		if msg.UserID != "42" {
			t.Errorf("expected userID '42', got %q", msg.UserID)
		}
		if msg.ChannelID != "99" {
			t.Errorf("expected channelID '99', got %q", msg.ChannelID)
		}
		if msg.Username != "alice" {
			t.Errorf("expected username 'alice', got %q", msg.Username)
		}
		if msg.Platform != "telegram" {
			t.Errorf("expected platform 'telegram', got %q", msg.Platform)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for message to be delivered to handler")
	}
}

// ---------------------------------------------------------------------------
// 3. Long-poll skips empty-text messages
// ---------------------------------------------------------------------------

func TestTelegramTransport_LongPoll_SkipsEmptyText(t *testing.T) {
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			updates := []tgUpdate{
				{
					UpdateID: 200,
					Message: &tgMessage{
						MessageID: 2,
						From:      tgUser{ID: 1, Username: "bob"},
						Chat:      tgChat{ID: 88},
						Text:      "", // sticker / file — no text
					},
				},
			}
			writeTgResponse(w, updates)
			return
		}
		<-r.Context().Done()
		writeTgResponse(w, []tgUpdate{})
	}))
	defer srv.Close()

	tr := newTestTelegramTransport(t, srv.URL)

	handlerCalled := make(chan struct{}, 1)
	tr.OnMessage(func(_ context.Context, msg InboundMessage) {
		handlerCalled <- struct{}{}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go tr.Start(ctx) //nolint:errcheck

	select {
	case <-handlerCalled:
		t.Fatal("handler should NOT have been called for empty-text message")
	case <-ctx.Done():
		// Expected: handler was never called.
	}
}

// ---------------------------------------------------------------------------
// 4. Long-poll advances offset
// ---------------------------------------------------------------------------

func TestTelegramTransport_LongPoll_AdvancesOffset(t *testing.T) {
	var callCount int32
	offsets := make(chan int64, 10)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)

		// Record offset sent by client.
		offsetStr := r.URL.Query().Get("offset")
		var off int64
		if offsetStr != "" {
			off = parseDecimal(offsetStr)
		}
		offsets <- off

		if n == 1 {
			updates := []tgUpdate{
				{UpdateID: 300, Message: &tgMessage{MessageID: 3, From: tgUser{ID: 1}, Chat: tgChat{ID: 1}, Text: "first"}},
				{UpdateID: 301, Message: &tgMessage{MessageID: 4, From: tgUser{ID: 1}, Chat: tgChat{ID: 1}, Text: "second"}},
			}
			writeTgResponse(w, updates)
			return
		}
		<-r.Context().Done()
		writeTgResponse(w, []tgUpdate{})
	}))
	defer srv.Close()

	tr := newTestTelegramTransport(t, srv.URL)
	tr.OnMessage(func(_ context.Context, msg InboundMessage) {})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go tr.Start(ctx) //nolint:errcheck

	// Wait for the second poll call (which should carry offset=302).
	var firstOffset, secondOffset int64
	select {
	case firstOffset = <-offsets:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first poll")
	}
	select {
	case secondOffset = <-offsets:
	case <-ctx.Done():
		t.Fatal("timed out waiting for second poll with advanced offset")
	}

	if firstOffset != 0 {
		t.Errorf("expected initial offset 0, got %d", firstOffset)
	}
	// After processing update_ids 300 and 301, offset should be 302.
	if secondOffset != 302 {
		t.Errorf("expected advanced offset 302, got %d", secondOffset)
	}
}

func parseDecimal(s string) int64 {
	var v int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		v = v*10 + int64(c-'0')
	}
	return v
}

// ---------------------------------------------------------------------------
// 5. Send — short message
// ---------------------------------------------------------------------------

func TestTelegramTransport_Send_ShortMessage(t *testing.T) {
	type sendBody struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}

	received := make(chan sendBody, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			writeTgResponse(w, true)
			return
		}
		var body sendBody
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		received <- body
		writeTgResponse(w, map[string]any{"message_id": 99})
	}))
	defer srv.Close()

	tr := newTestTelegramTransport(t, srv.URL)

	ctx := context.Background()
	err := tr.Send(ctx, OutboundMessage{
		ChannelID: "12345",
		Text:      "Hello, world!",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	select {
	case body := <-received:
		if body.ChatID != 12345 {
			t.Errorf("expected chat_id 12345, got %d", body.ChatID)
		}
		if body.Text != "Hello, world!" {
			t.Errorf("expected text 'Hello, world!', got %q", body.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sendMessage call")
	}
}

// ---------------------------------------------------------------------------
// 6. Send — long message is split
// ---------------------------------------------------------------------------

func TestTelegramTransport_Send_LongMessage(t *testing.T) {
	var sendCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			atomic.AddInt32(&sendCount, 1)
		}
		writeTgResponse(w, map[string]any{"message_id": 1})
	}))
	defer srv.Close()

	tr := newTestTelegramTransport(t, srv.URL)

	// 4097 chars → should split into 2 messages (4096 + 1).
	longText := strings.Repeat("a", telegramMaxTextLen+1)

	ctx := context.Background()
	err := tr.Send(ctx, OutboundMessage{
		ChannelID: "99",
		Text:      longText,
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if got := atomic.LoadInt32(&sendCount); got != 2 {
		t.Errorf("expected 2 sendMessage calls for long text, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// 7. Send — Markdown mode
// ---------------------------------------------------------------------------

func TestTelegramTransport_Send_MarkdownMode(t *testing.T) {
	type sendBody struct {
		ParseMode string `json:"parse_mode"`
	}

	received := make(chan sendBody, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			writeTgResponse(w, true)
			return
		}
		var body sendBody
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		received <- body
		writeTgResponse(w, map[string]any{"message_id": 7})
	}))
	defer srv.Close()

	tr := newTestTelegramTransport(t, srv.URL)

	ctx := context.Background()
	err := tr.Send(ctx, OutboundMessage{
		ChannelID: "55",
		Text:      "**bold**",
		Markdown:  true,
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	select {
	case body := <-received:
		if body.ParseMode != "Markdown" {
			t.Errorf("expected parse_mode 'Markdown', got %q", body.ParseMode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sendMessage call")
	}
}
