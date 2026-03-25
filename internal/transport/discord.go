package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

const (
	discordAPIBase = "https://discord.com/api/v10"
	discordIntents = 33280 // GUILDS(1) | GUILD_MESSAGES(512) | MESSAGE_CONTENT(32768)

	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResumeSession  = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11

	discordMaxMsgLen = 2000
)

// DiscordConfig configures the Discord transport.
type DiscordConfig struct {
	Token string // Bot token ("Bot " prefix is added automatically)
	AppID string // Application ID (for slash command registration)
}

// DiscordTransport implements Transport for the Discord Gateway API.
type DiscordTransport struct {
	token      string
	appID      string
	handler    func(ctx context.Context, msg InboundMessage)
	httpClient *http.Client
	conn       *websocket.Conn
	connMu     sync.Mutex
	sessionID  string
	resumeURL  string
	sequence   atomic.Int64
	heartbeat  *time.Ticker
	stopCh     chan struct{}
	logger     zerolog.Logger
	// gatewayURLOverride allows tests to replace the gateway URL lookup.
	gatewayURLOverride string
	// webhooks caches channel ID → webhook URL for persona messages.
	webhooks   map[string]string
	webhooksMu sync.Mutex
}

// Internal Discord wire types.
type gatewayPayload struct {
	OP        int             `json:"op"`
	Data      json.RawMessage `json:"d"`
	Sequence  *int64          `json:"s"`
	EventName string          `json:"t"`
}

type discordMessage struct {
	ID                string          `json:"id"`
	ChannelID         string          `json:"channel_id"`
	Content           string          `json:"content"`
	Author            discordUser     `json:"author"`
	ReferencedMessage *discordMessage `json:"referenced_message"`
}

type discordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

// NewDiscordTransport creates a new Discord transport.
func NewDiscordTransport(cfg DiscordConfig, logger zerolog.Logger) *DiscordTransport {
	return &DiscordTransport{
		token:      cfg.Token,
		appID:      cfg.AppID,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		stopCh:     make(chan struct{}),
		logger:     logger,
	}
}

// Name returns the transport identifier.
func (t *DiscordTransport) Name() string { return "discord" }

// OnMessage registers the inbound message handler. Must be called before Start.
func (t *DiscordTransport) OnMessage(handler func(ctx context.Context, msg InboundMessage)) {
	t.handler = handler
}

// Start connects to the Discord Gateway and begins receiving events.
// It blocks until ctx is cancelled or a fatal error occurs.
func (t *DiscordTransport) Start(ctx context.Context) error {
	gwURL, err := t.getGatewayURL(ctx)
	if err != nil {
		return fmt.Errorf("discord: get gateway url: %w", err)
	}

	if err := t.connect(ctx, gwURL); err != nil {
		return fmt.Errorf("discord: connect: %w", err)
	}

	t.registerSlashCommands(ctx)

	return t.readLoop(ctx, gwURL)
}

// registerSlashCommands registers global slash commands with Discord.
// Uses bulk overwrite (PUT) which is idempotent — safe to call on every startup.
func (t *DiscordTransport) registerSlashCommands(ctx context.Context) {
	if t.appID == "" {
		t.logger.Warn().Msg("discord: no app ID configured, skipping slash command registration")
		return
	}

	commands := []map[string]any{
		{
			"name":        "default_role",
			"description": "Set the default persona or tag that responds in this channel",
			"options": []map[string]any{
				{
					"name":        "role",
					"description": "Persona username, tag name, or 'none' to clear",
					"type":        3, // STRING
					"required":    false,
				},
			},
		},
		{
			"name":        "chat",
			"description": "Switch to chat mode (no tools — faster & cheaper)",
		},
		{
			"name":        "execute",
			"description": "Switch to execute mode (full tools enabled)",
		},
		{
			"name":        "mode",
			"description": "Show or switch the current mode",
			"options": []map[string]any{
				{
					"name":        "name",
					"description": "Mode name to switch to (omit to show current)",
					"type":        3, // STRING
					"required":    false,
				},
			},
		},
	}

	data, _ := json.Marshal(commands)
	url := discordAPIBase + "/applications/" + t.appID + "/commands"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		t.logger.Warn().Err(err).Msg("discord: failed to create slash command request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+t.token)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		t.logger.Warn().Err(err).Msg("discord: failed to register slash commands")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		t.logger.Info().Msg("discord: slash commands registered")
	} else {
		body, _ := io.ReadAll(resp.Body)
		t.logger.Warn().Int("status", resp.StatusCode).Str("body", string(body)).Msg("discord: slash command registration failed")
	}
}

// Stop gracefully shuts down the transport.
func (t *DiscordTransport) Stop(_ context.Context) error {
	select {
	case <-t.stopCh:
		// already stopped
	default:
		close(t.stopCh)
	}
	t.connMu.Lock()
	defer t.connMu.Unlock()
	if t.conn != nil {
		err := t.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}

// getGatewayURL fetches the WebSocket gateway URL from Discord REST API.
func (t *DiscordTransport) getGatewayURL(ctx context.Context) (string, error) {
	if t.gatewayURLOverride != "" {
		return t.gatewayURLOverride, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordAPIBase+"/gateway/bot", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+t.token)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord: gateway/bot returned %d", resp.StatusCode)
	}

	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("discord: decode gateway response: %w", err)
	}
	if body.URL == "" {
		return "", fmt.Errorf("discord: empty gateway url")
	}
	return body.URL, nil
}

// connect opens the WebSocket connection and performs the handshake.
func (t *DiscordTransport) connect(ctx context.Context, gwURL string) error {
	fullURL := gwURL + "?v=10&encoding=json"
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, fullURL, nil)
	if err != nil {
		return fmt.Errorf("discord: dial %s: %w", fullURL, err)
	}

	t.connMu.Lock()
	t.conn = conn
	t.connMu.Unlock()

	// Receive OP 10 Hello.
	var hello gatewayPayload
	if err := conn.ReadJSON(&hello); err != nil {
		return fmt.Errorf("discord: read hello: %w", err)
	}
	if hello.OP != opHello {
		return fmt.Errorf("discord: expected OP 10, got %d", hello.OP)
	}

	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.Data, &helloData); err != nil {
		return fmt.Errorf("discord: decode hello data: %w", err)
	}

	interval := time.Duration(helloData.HeartbeatInterval) * time.Millisecond
	t.startHeartbeat(conn, interval)

	// Send OP 2 Identify.
	identify := gatewayPayload{
		OP: opIdentify,
		Data: mustMarshal(map[string]interface{}{
			"token":   "Bot " + t.token,
			"intents": discordIntents,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "capabot",
				"device":  "capabot",
			},
		}),
	}
	t.connMu.Lock()
	err = conn.WriteJSON(identify)
	t.connMu.Unlock()
	if err != nil {
		return fmt.Errorf("discord: send identify: %w", err)
	}

	// Receive OP 0 READY.
	var ready gatewayPayload
	if err := conn.ReadJSON(&ready); err != nil {
		return fmt.Errorf("discord: read ready: %w", err)
	}
	if ready.OP == opDispatch && ready.EventName == "READY" {
		var readyData struct {
			SessionID string `json:"session_id"`
			ResumeURL string `json:"resume_gateway_url"`
		}
		if err := json.Unmarshal(ready.Data, &readyData); err != nil {
			return fmt.Errorf("discord: decode ready data: %w", err)
		}
		t.sessionID = readyData.SessionID
		t.resumeURL = readyData.ResumeURL
		t.logger.Info().Str("session_id", t.sessionID).Msg("discord: gateway ready")
	}

	return nil
}

// startHeartbeat launches a goroutine that sends OP 1 at the given interval.
func (t *DiscordTransport) startHeartbeat(conn *websocket.Conn, interval time.Duration) {
	if t.heartbeat != nil {
		t.heartbeat.Stop()
	}
	ticker := time.NewTicker(interval)
	t.heartbeat = ticker

	go func() {
		for {
			select {
			case <-ticker.C:
				seq := t.sequence.Load()
				var seqVal interface{} = seq
				if seq == 0 {
					seqVal = nil
				}
				payload := gatewayPayload{
					OP:   opHeartbeat,
					Data: mustMarshal(seqVal),
				}
				t.connMu.Lock()
				writeErr := conn.WriteJSON(payload)
				t.connMu.Unlock()
				if writeErr != nil {
					t.logger.Error().Err(writeErr).Msg("discord: heartbeat write error")
					return
				}
			case <-t.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// readLoop processes incoming gateway events until ctx is cancelled.
func (t *DiscordTransport) readLoop(ctx context.Context, gwURL string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.stopCh:
			return nil
		default:
		}

		t.connMu.Lock()
		conn := t.conn
		t.connMu.Unlock()

		var payload gatewayPayload
		if err := conn.ReadJSON(&payload); err != nil {
			select {
			case <-t.stopCh:
				return nil
			default:
			}
			t.logger.Warn().Err(err).Msg("discord: read error, reconnecting")
			if reconnErr := t.reconnect(ctx, gwURL); reconnErr != nil {
				return fmt.Errorf("discord: reconnect failed: %w", reconnErr)
			}
			continue
		}

		if payload.Sequence != nil {
			t.sequence.Store(*payload.Sequence)
		}

		t.handlePayload(ctx, payload, gwURL)
	}
}

// handlePayload dispatches a single gateway payload.
func (t *DiscordTransport) handlePayload(ctx context.Context, p gatewayPayload, gwURL string) {
	switch p.OP {
	case opDispatch:
		t.handleDispatch(ctx, p)
	case opHeartbeatAck:
		t.logger.Debug().Msg("discord: heartbeat ack")
	case opReconnect:
		t.logger.Info().Msg("discord: server requested reconnect")
		go func() {
			if err := t.resume(ctx); err != nil {
				t.logger.Error().Err(err).Msg("discord: resume failed after OP 7")
			}
		}()
	case opInvalidSession:
		t.logger.Warn().Msg("discord: invalid session, re-identifying")
		delay := time.Duration(1+rand.Intn(4)) * time.Second
		time.Sleep(delay)
		go func() {
			if err := t.connect(ctx, gwURL); err != nil {
				t.logger.Error().Err(err).Msg("discord: re-identify failed")
			}
		}()
	}
}

// handleDispatch processes OP 0 dispatch events.
func (t *DiscordTransport) handleDispatch(ctx context.Context, p gatewayPayload) {
	switch p.EventName {
	case "MESSAGE_CREATE":
		t.handleMessageCreate(ctx, p)
	case "INTERACTION_CREATE":
		t.handleInteractionCreate(ctx, p)
	}
}

func (t *DiscordTransport) handleMessageCreate(ctx context.Context, p gatewayPayload) {
	var dm discordMessage
	if err := json.Unmarshal(p.Data, &dm); err != nil {
		t.logger.Error().Err(err).Msg("discord: decode MESSAGE_CREATE")
		return
	}
	if dm.Author.Bot {
		return
	}
	if t.handler == nil {
		return
	}

	msg := InboundMessage{
		ID:        dm.ID,
		ChannelID: dm.ChannelID,
		UserID:    dm.Author.ID,
		Username:  dm.Author.Username,
		Text:      dm.Content,
		Platform:  "discord",
	}
	if dm.ReferencedMessage != nil {
		msg.ReplyToID = dm.ReferencedMessage.ID
	}

	go t.handler(ctx, msg)
}

// handleInteractionCreate handles Discord slash command interactions.
// Converts them into synthetic InboundMessages so the existing handler can process them.
func (t *DiscordTransport) handleInteractionCreate(ctx context.Context, p gatewayPayload) {
	var interaction struct {
		ID        string `json:"id"`
		Token     string `json:"token"`
		ChannelID string `json:"channel_id"`
		Member    *struct {
			User discordUser `json:"user"`
		} `json:"member"`
		User *discordUser `json:"user"` // DM interactions
		Data struct {
			Name    string `json:"name"`
			Options []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"options"`
		} `json:"data"`
	}
	if err := json.Unmarshal(p.Data, &interaction); err != nil {
		t.logger.Error().Err(err).Msg("discord: decode INTERACTION_CREATE")
		return
	}

	// ACK with deferred response — we'll send the real response via the message handler.
	t.ackInteraction(ctx, interaction.ID, interaction.Token)

	// Build the command text.
	text := "/" + interaction.Data.Name
	for _, opt := range interaction.Data.Options {
		text += " " + opt.Value
	}

	user := discordUser{}
	if interaction.Member != nil {
		user = interaction.Member.User
	} else if interaction.User != nil {
		user = *interaction.User
	}

	msg := InboundMessage{
		ID:        interaction.ID,
		ChannelID: interaction.ChannelID,
		UserID:    user.ID,
		Username:  user.Username,
		Text:      text,
		Platform:  "discord",
	}

	if t.handler != nil {
		go t.handler(ctx, msg)
	}
}

// ackInteraction acknowledges a slash command with a brief message.
// The real response is sent by the message handler via t.Send().
func (t *DiscordTransport) ackInteraction(ctx context.Context, interactionID, interactionToken string) {
	// Type 4 = CHANNEL_MESSAGE_WITH_SOURCE (immediate visible response).
	body, _ := json.Marshal(map[string]any{
		"type": 4,
		"data": map[string]any{
			"content": "...",
			"flags":   64, // EPHEMERAL — only visible to the command user
		},
	})
	url := discordAPIBase + "/interactions/" + interactionID + "/" + interactionToken + "/callback"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		t.logger.Warn().Err(err).Msg("discord: failed to ACK interaction")
		return
	}
	resp.Body.Close()
}

// resume attempts to resume an existing session.
func (t *DiscordTransport) resume(ctx context.Context) error {
	url := t.resumeURL
	if url == "" {
		return fmt.Errorf("discord: no resume url")
	}
	fullURL := url + "?v=10&encoding=json"
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, fullURL, nil)
	if err != nil {
		return fmt.Errorf("discord: resume dial: %w", err)
	}

	t.connMu.Lock()
	if t.conn != nil {
		t.conn.Close()
	}
	t.conn = conn
	t.connMu.Unlock()

	resumePayload := gatewayPayload{
		OP: opResumeSession,
		Data: mustMarshal(map[string]interface{}{
			"token":      "Bot " + t.token,
			"session_id": t.sessionID,
			"seq":        t.sequence.Load(),
		}),
	}
	t.connMu.Lock()
	err = conn.WriteJSON(resumePayload)
	t.connMu.Unlock()
	return err
}

// reconnect tries to resume, falling back to a full reconnect.
func (t *DiscordTransport) reconnect(ctx context.Context, gwURL string) error {
	if t.sessionID != "" {
		if err := t.resume(ctx); err == nil {
			return nil
		}
		t.logger.Warn().Msg("discord: resume failed, doing full reconnect")
	}
	return t.connect(ctx, gwURL)
}

// mustMarshal marshals v to JSON, panicking on error (only used with known-good types).
func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("discord: mustMarshal: %v", err))
	}
	return b
}
