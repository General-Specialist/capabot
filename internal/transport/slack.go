package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

const (
	slackMaxTextLen           = 3000
	slackConnectionsOpenURL   = "https://slack.com/api/apps.connections.open"
	slackChatPostMessageURL   = "https://slack.com/api/chat.postMessage"
	slackBackoffInitial       = time.Second
	slackBackoffMax           = 30 * time.Second
)

// SlackConfig configures the Slack Socket Mode transport.
type SlackConfig struct {
	AppToken string // xapp- prefix; used for Socket Mode connection
	BotToken string // xoxb- prefix; used for sending messages
	Addr     string // kept for interface consistency; not used in Socket Mode
}

// SlackTransport implements Transport using Slack's Socket Mode (WebSocket).
type SlackTransport struct {
	cfg        SlackConfig
	handler    func(ctx context.Context, msg InboundMessage)
	httpClient *http.Client
	conn       *websocket.Conn
	connMu     sync.Mutex
	stopCh     chan struct{}
	stopOnce   sync.Once
	logger     zerolog.Logger

	// overridable for testing
	connectionsOpenURL string
	postMessageURL     string
}

// NewSlackTransport creates a new Slack Socket Mode transport.
func NewSlackTransport(cfg SlackConfig, logger zerolog.Logger) *SlackTransport {
	return &SlackTransport{
		cfg:                cfg,
		httpClient:         &http.Client{Timeout: 30 * time.Second},
		stopCh:             make(chan struct{}),
		logger:             logger,
		connectionsOpenURL: slackConnectionsOpenURL,
		postMessageURL:     slackChatPostMessageURL,
	}
}

// Name returns the transport identifier.
func (s *SlackTransport) Name() string { return "slack" }

// OnMessage registers the handler called for every inbound message.
// Must be called before Start.
func (s *SlackTransport) OnMessage(handler func(ctx context.Context, msg InboundMessage)) {
	s.handler = handler
}

// Start connects via Socket Mode and processes events. Blocks until ctx is
// cancelled or Stop is called.
func (s *SlackTransport) Start(ctx context.Context) error {
	s.logger.Info().Msg("slack: starting socket mode")

	backoff := slackBackoffInitial

	for {
		// Check for stop/cancel before each connection attempt.
		select {
		case <-ctx.Done():
			return nil
		case <-s.stopCh:
			return nil
		default:
		}

		wsURL, err := s.openConnection(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Error().Err(err).Dur("backoff", backoff).Msg("slack: failed to open connection, backing off")
			if !s.sleep(ctx, backoff) {
				return nil
			}
			backoff = minDuration(backoff*2, slackBackoffMax)
			continue
		}

		// Reset backoff after a successful connection.
		backoff = slackBackoffInitial

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Error().Err(err).Msg("slack: websocket dial failed")
			if !s.sleep(ctx, backoff) {
				return nil
			}
			backoff = minDuration(backoff*2, slackBackoffMax)
			continue
		}

		s.connMu.Lock()
		s.conn = conn
		s.connMu.Unlock()

		s.logger.Info().Msg("slack: websocket connected")

		// readLoop returns when the connection drops or context is cancelled.
		s.readLoop(ctx, conn)

		s.connMu.Lock()
		s.conn = nil
		s.connMu.Unlock()

		// Exit cleanly if requested.
		select {
		case <-ctx.Done():
			return nil
		case <-s.stopCh:
			return nil
		default:
		}

		s.logger.Warn().Dur("backoff", backoff).Msg("slack: disconnected, reconnecting")
		if !s.sleep(ctx, backoff) {
			return nil
		}
		backoff = minDuration(backoff*2, slackBackoffMax)
	}
}

// Stop gracefully shuts down the transport.
func (s *SlackTransport) Stop(_ context.Context) error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.connMu.Lock()
		if s.conn != nil {
			_ = s.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			)
			_ = s.conn.Close()
		}
		s.connMu.Unlock()
	})
	return nil
}

// Send posts a message to Slack, splitting if text exceeds 3000 chars.
func (s *SlackTransport) Send(ctx context.Context, msg OutboundMessage) error {
	chunks := splitText(msg.Text, slackMaxTextLen)
	for _, chunk := range chunks {
		if err := s.postMessage(ctx, msg.ChannelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// openConnection calls apps.connections.open and returns the WebSocket URL.
func (s *SlackTransport) openConnection(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.connectionsOpenURL, nil)
	if err != nil {
		return "", fmt.Errorf("slack: create connections.open request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.AppToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("slack: connections.open: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("slack: read connections.open response: %w", err)
	}

	var result struct {
		OK    bool   `json:"ok"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("slack: decode connections.open response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("slack: connections.open returned ok=false: %s", result.Error)
	}
	return result.URL, nil
}

// slackEnvelope is the outer wrapper for Socket Mode events.
type slackEnvelope struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Payload    json.RawMessage `json:"payload"`
}

// slackEventsAPIPayload is the payload for events_api envelope type.
type slackEventsAPIPayload struct {
	Event slackEvent `json:"event"`
}

// slackEvent represents a Slack event inside an events_api payload.
type slackEvent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Channel string `json:"channel"`
	User    string `json:"user"`
	EventTS string `json:"event_ts"`
}

// readLoop reads messages from the WebSocket connection until it closes.
func (s *SlackTransport) readLoop(ctx context.Context, conn *websocket.Conn) {
	// Close connection when context is cancelled so ReadMessage unblocks.
	go func() {
		select {
		case <-ctx.Done():
		case <-s.stopCh:
		}
		_ = conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil || s.isStopped() {
				return
			}
			s.logger.Error().Err(err).Msg("slack: websocket read error")
			return
		}

		if err := s.handleEnvelope(ctx, conn, data); err != nil {
			s.logger.Error().Err(err).Msg("slack: handle envelope error")
		}
	}
}

// handleEnvelope parses and dispatches a single Socket Mode envelope.
func (s *SlackTransport) handleEnvelope(ctx context.Context, conn *websocket.Conn, data []byte) error {
	var env slackEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("slack: decode envelope: %w", err)
	}

	switch env.Type {
	case "hello":
		s.logger.Debug().Msg("slack: received hello")
	case "events_api":
		if err := s.handleEventsAPI(ctx, conn, env); err != nil {
			return err
		}
	default:
		s.logger.Debug().Str("type", env.Type).Msg("slack: unhandled envelope type")
	}
	return nil
}

// handleEventsAPI processes an events_api envelope, dispatches to the handler,
// and sends the acknowledgement.
func (s *SlackTransport) handleEventsAPI(ctx context.Context, conn *websocket.Conn, env slackEnvelope) error {
	// Acknowledge first (Slack requires ack within 3 seconds).
	if err := s.acknowledge(conn, env.EnvelopeID); err != nil {
		return fmt.Errorf("slack: acknowledge: %w", err)
	}

	if len(env.Payload) == 0 {
		return nil
	}

	var payload slackEventsAPIPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("slack: decode events_api payload: %w", err)
	}

	if payload.Event.Type != "message" {
		return nil
	}
	if payload.Event.Text == "" {
		return nil
	}
	if s.handler == nil {
		return nil
	}

	inbound := InboundMessage{
		ID:        payload.Event.EventTS,
		ChannelID: payload.Event.Channel,
		UserID:    payload.Event.User,
		Text:      payload.Event.Text,
		Platform:  "slack",
	}
	s.handler(ctx, inbound)
	return nil
}

// acknowledge sends the Socket Mode acknowledgement for an envelope.
func (s *SlackTransport) acknowledge(conn *websocket.Conn, envelopeID string) error {
	ack := map[string]any{
		"envelope_id": envelopeID,
		"payload":     map[string]string{"text": "ok"},
	}
	data, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("slack: marshal ack: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("slack: write ack: %w", err)
	}
	return nil
}

// postMessage sends a single chunk to the Slack chat.postMessage API.
func (s *SlackTransport) postMessage(ctx context.Context, channelID, text string) error {
	body := map[string]string{
		"channel": channelID,
		"text":    text,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("slack: marshal postMessage body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.postMessageURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("slack: create postMessage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.BotToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack: postMessage http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("slack: read postMessage response: %w", err)
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("slack: decode postMessage response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("slack: chat.postMessage returned ok=false: %s", result.Error)
	}
	return nil
}

// sleep waits for the given duration, returning false if ctx or stopCh fires.
func (s *SlackTransport) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

// isStopped reports whether Stop has been called.
func (s *SlackTransport) isStopped() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
