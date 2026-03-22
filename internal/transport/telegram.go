package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

const (
	telegramAPIBase     = "https://api.telegram.org/bot"
	telegramMaxTextLen  = 4096
	telegramPollTimeout = 30
	telegramBackoff     = time.Second
)

// TelegramTransport implements Transport using the Telegram Bot API.
// Supports both long-polling (development) and webhook (production) modes.
type TelegramTransport struct {
	token      string
	webhookURL string // if set, use webhook mode; otherwise long-poll
	addr       string // local addr for webhook server
	handler    func(ctx context.Context, msg InboundMessage)
	httpClient *http.Client
	server     *http.Server // for webhook mode
	stopCh     chan struct{}
	logger     zerolog.Logger
}

// TelegramConfig configures the Telegram transport.
type TelegramConfig struct {
	Token      string // Bot API token from @BotFather
	WebhookURL string // e.g. "https://myserver.com/telegram/webhook" — empty = long-poll
	Addr       string // local addr for webhook server, e.g. ":8443"
}

// NewTelegramTransport creates a new Telegram transport.
func NewTelegramTransport(cfg TelegramConfig, logger zerolog.Logger) *TelegramTransport {
	return &TelegramTransport{
		token:      cfg.Token,
		webhookURL: cfg.WebhookURL,
		addr:       cfg.Addr,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		stopCh:     make(chan struct{}),
		logger:     logger,
	}
}

// Name returns the transport identifier.
func (t *TelegramTransport) Name() string { return "telegram" }

// OnMessage registers the handler called for every inbound message.
func (t *TelegramTransport) OnMessage(handler func(ctx context.Context, msg InboundMessage)) {
	t.handler = handler
}

// Start begins accepting messages. Blocks until ctx is cancelled or an error occurs.
func (t *TelegramTransport) Start(ctx context.Context) error {
	if t.webhookURL != "" {
		return t.startWebhook(ctx)
	}
	return t.startLongPoll(ctx)
}

// Stop gracefully shuts down the transport.
func (t *TelegramTransport) Stop(ctx context.Context) error {
	select {
	case <-t.stopCh:
		// already closed
	default:
		close(t.stopCh)
	}

	if t.server != nil {
		if err := t.deleteWebhook(ctx); err != nil {
			t.logger.Warn().Err(err).Msg("telegram: failed to delete webhook on stop")
		}
		return t.server.Shutdown(ctx)
	}
	return nil
}

// Send delivers an outbound message, splitting if text exceeds Telegram's 4096-char limit.
func (t *TelegramTransport) Send(ctx context.Context, msg OutboundMessage) error {
	chunks := splitText(msg.Text, telegramMaxTextLen)
	for i, chunk := range chunks {
		if err := t.sendChunk(ctx, msg, chunk, i); err != nil {
			return err
		}
	}
	return nil
}

func (t *TelegramTransport) sendChunk(ctx context.Context, msg OutboundMessage, text string, chunkIndex int) error {
	chatID, err := strconv.ParseInt(msg.ChannelID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: invalid channel_id %q: %w", msg.ChannelID, err)
	}

	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if msg.Markdown {
		body["parse_mode"] = "Markdown"
	}
	// Only attach reply_to on the first chunk.
	if msg.ReplyToID != "" && chunkIndex == 0 {
		replyID, err := strconv.ParseInt(msg.ReplyToID, 10, 64)
		if err == nil {
			body["reply_to_message_id"] = replyID
		}
	}

	if err := t.apiPost(ctx, "sendMessage", body, nil); err != nil {
		return fmt.Errorf("telegram: sendMessage: %w", err)
	}
	return nil
}

// startLongPoll runs the getUpdates polling loop.
func (t *TelegramTransport) startLongPoll(ctx context.Context) error {
	t.logger.Info().Msg("telegram: starting long-poll mode")
	var offset int64

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.stopCh:
			return nil
		default:
		}

		updates, err := t.getUpdates(ctx, offset)
		if err != nil {
			// ctx cancelled — clean exit
			if ctx.Err() != nil {
				return nil
			}
			t.logger.Error().Err(err).Msg("telegram: getUpdates error, backing off")
			select {
			case <-ctx.Done():
				return nil
			case <-t.stopCh:
				return nil
			case <-time.After(telegramBackoff):
			}
			continue
		}

		for _, upd := range updates {
			if int64(upd.UpdateID) >= offset {
				offset = int64(upd.UpdateID) + 1
			}
			t.handleUpdate(ctx, upd)
		}
	}
}

// getUpdates calls the Telegram getUpdates endpoint.
func (t *TelegramTransport) getUpdates(ctx context.Context, offset int64) ([]tgUpdate, error) {
	url := fmt.Sprintf("%s%s/getUpdates?offset=%d&timeout=%d&allowed_updates=[\"message\",\"callback_query\"]",
		telegramAPIBase, t.token, offset, telegramPollTimeout)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	defer resp.Body.Close()

	var tgResp tgResponse
	if err := json.NewDecoder(resp.Body).Decode(&tgResp); err != nil {
		return nil, fmt.Errorf("telegram: decode getUpdates response: %w", err)
	}
	if !tgResp.OK {
		return nil, fmt.Errorf("telegram: getUpdates returned ok=false")
	}

	var updates []tgUpdate
	if err := json.Unmarshal(tgResp.Result, &updates); err != nil {
		return nil, fmt.Errorf("telegram: unmarshal updates: %w", err)
	}
	return updates, nil
}

// handleUpdate dispatches a single Telegram update to the registered handler.
func (t *TelegramTransport) handleUpdate(ctx context.Context, upd tgUpdate) {
	if upd.Message == nil {
		return
	}
	msg := upd.Message
	if msg.Text == "" {
		// Skip non-text messages (stickers, files, etc.)
		return
	}
	if t.handler == nil {
		return
	}

	inbound := InboundMessage{
		ID:        strconv.FormatInt(int64(msg.MessageID), 10),
		ChannelID: strconv.FormatInt(msg.Chat.ID, 10),
		UserID:    strconv.FormatInt(msg.From.ID, 10),
		Username:  msg.From.Username,
		Text:      msg.Text,
		Platform:  "telegram",
	}
	if msg.ReplyToMessage != nil {
		inbound.ReplyToID = strconv.FormatInt(int64(msg.ReplyToMessage.MessageID), 10)
	}

	t.handler(ctx, inbound)
}

// startWebhook registers the webhook and starts a local HTTP server.
func (t *TelegramTransport) startWebhook(ctx context.Context) error {
	t.logger.Info().Str("url", t.webhookURL).Msg("telegram: registering webhook")

	if err := t.setWebhook(ctx); err != nil {
		return fmt.Errorf("telegram: setWebhook: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var upd tgUpdate
		if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
			t.logger.Error().Err(err).Msg("telegram: webhook decode error")
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		t.handleUpdate(r.Context(), upd)
		w.WriteHeader(http.StatusOK)
	})

	t.server = &http.Server{
		Addr:    t.addr,
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		t.logger.Info().Str("addr", t.addr).Msg("telegram: webhook server listening")
		if err := t.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return t.Stop(context.Background())
	case <-t.stopCh:
		return nil
	case err := <-errCh:
		return fmt.Errorf("telegram: webhook server: %w", err)
	}
}

// setWebhook calls the Telegram setWebhook API.
func (t *TelegramTransport) setWebhook(ctx context.Context) error {
	body := map[string]string{"url": t.webhookURL}
	return t.apiPost(ctx, "setWebhook", body, nil)
}

// deleteWebhook removes the registered webhook.
func (t *TelegramTransport) deleteWebhook(ctx context.Context) error {
	return t.apiPost(ctx, "deleteWebhook", map[string]any{}, nil)
}

// apiPost sends a POST request to a Telegram Bot API method.
// If result is non-nil, the tgResponse.Result is unmarshalled into it.
func (t *TelegramTransport) apiPost(ctx context.Context, method string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s%s/%s", telegramAPIBase, t.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	var tgResp tgResponse
	if err := json.NewDecoder(resp.Body).Decode(&tgResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !tgResp.OK {
		return fmt.Errorf("api returned ok=false for method %q", method)
	}
	if result != nil && tgResp.Result != nil {
		if err := json.Unmarshal(tgResp.Result, result); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}
	return nil
}

// splitText divides text into chunks of at most maxLen runes each.
func splitText(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(runes) > 0 {
		end := maxLen
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}

// ---------------------------------------------------------------------------
// Internal Telegram API types
// ---------------------------------------------------------------------------

type tgUpdate struct {
	UpdateID int        `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgMessage struct {
	MessageID      int        `json:"message_id"`
	From           tgUser     `json:"from"`
	Chat           tgChat     `json:"chat"`
	Text           string     `json:"text"`
	ReplyToMessage *tgMessage `json:"reply_to_message"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
}
