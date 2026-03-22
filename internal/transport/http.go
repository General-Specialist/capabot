package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// HTTPTransport implements Transport via a RESTful HTTP API.
type HTTPTransport struct {
	addr    string
	apiKeys map[string]bool
	handler func(ctx context.Context, msg InboundMessage)
	server  *http.Server
	mux     *http.ServeMux
	pending sync.Map // requestID → chan string (response channel)
	logger  zerolog.Logger
}

// HTTPConfig configures the HTTP transport.
type HTTPConfig struct {
	Addr    string   // e.g., ":8080"
	APIKeys []string // if non-empty, Bearer token auth is required
}

// NewHTTPTransport creates a new HTTP transport.
func NewHTTPTransport(cfg HTTPConfig, logger zerolog.Logger) *HTTPTransport {
	keys := make(map[string]bool, len(cfg.APIKeys))
	for _, k := range cfg.APIKeys {
		keys[k] = true
	}
	t := &HTTPTransport{
		addr:    cfg.Addr,
		apiKeys: keys,
		logger:  logger,
		mux:     http.NewServeMux(),
	}

	t.mux.HandleFunc("GET /healthz", t.handleHealthz)
	t.mux.HandleFunc("POST /v1/chat", t.handleChat)

	t.server = &http.Server{
		Addr:    cfg.Addr,
		Handler: t.mux,
	}
	return t
}

// Handler returns the underlying http.Handler for use in tests.
func (t *HTTPTransport) Handler() http.Handler { return t.mux }

// ServeHTTP implements http.Handler for direct use with httptest.
func (t *HTTPTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mux.ServeHTTP(w, r)
}

func (t *HTTPTransport) Name() string { return "http" }

func (t *HTTPTransport) OnMessage(handler func(ctx context.Context, msg InboundMessage)) {
	t.handler = handler
}

func (t *HTTPTransport) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		t.logger.Info().Str("addr", t.addr).Msg("HTTP transport listening")
		if err := t.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return t.Stop(context.Background())
	case err := <-errCh:
		return fmt.Errorf("http transport: %w", err)
	}
}

func (t *HTTPTransport) Stop(ctx context.Context) error {
	t.logger.Info().Msg("HTTP transport shutting down")
	return t.server.Shutdown(ctx)
}

// Send delivers a response to the pending request identified by msg.ChannelID.
func (t *HTTPTransport) Send(ctx context.Context, msg OutboundMessage) error {
	ch, ok := t.pending.Load(msg.ChannelID)
	if !ok {
		return fmt.Errorf("no pending request for channel %q", msg.ChannelID)
	}
	responseCh := ch.(chan string)
	select {
	case responseCh <- msg.Text:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *HTTPTransport) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (t *HTTPTransport) handleChat(w http.ResponseWriter, r *http.Request) {
	if !t.authorize(r) {
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
		Text      string `json:"text"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		writeError(w, "text is required", http.StatusBadRequest)
		return
	}

	// Use a unique request ID as the channel ID so Send() can route back.
	requestID := uuid.New().String()
	if req.ChannelID == "" {
		req.ChannelID = requestID
	}

	responseCh := make(chan string, 1)
	t.pending.Store(requestID, responseCh)
	defer t.pending.Delete(requestID)

	if t.handler == nil {
		writeError(w, "no message handler registered", http.StatusInternalServerError)
		return
	}

	msg := InboundMessage{
		ID:        requestID,
		ChannelID: requestID, // responses keyed by requestID
		UserID:    req.UserID,
		Text:      req.Text,
		Platform:  "http",
	}

	go t.handler(r.Context(), msg)

	select {
	case response := <-responseCh:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"response": response})
	case <-r.Context().Done():
		writeError(w, "request cancelled", http.StatusRequestTimeout)
	}
}

func (t *HTTPTransport) authorize(r *http.Request) bool {
	if len(t.apiKeys) == 0 {
		return true
	}
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	return t.apiKeys[token]
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
