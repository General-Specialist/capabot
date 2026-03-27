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

	"github.com/bwmarrin/discordgo"
	"github.com/rs/zerolog"
)

const (
	discordAPIBase   = "https://discord.com/api/v10"
	discordMaxMsgLen = 2000
)

// DiscordConfig configures the Discord transport.
type DiscordConfig struct {
	Token string // Bot token
	AppID string // Application ID (for slash command registration)
}

// DiscordTransport implements Transport for Discord using the discordgo library.
// discordgo handles gateway connection, heartbeat, resume, and reconnect automatically.
type DiscordTransport struct {
	session    *discordgo.Session
	token      string // raw token (no "Bot " prefix) for REST API calls
	appID      string
	handler    func(ctx context.Context, msg InboundMessage)
	httpClient *http.Client
	logger     zerolog.Logger
	stopCh     chan struct{}
	// webhooks caches channel+person → webhook URL for person messages.
	webhooks   map[string]string
	webhooksMu sync.Mutex
}

// NewDiscordTransport creates a new Discord transport.
func NewDiscordTransport(cfg DiscordConfig, logger zerolog.Logger) *DiscordTransport {
	t := &DiscordTransport{
		token:      cfg.Token,
		appID:      cfg.AppID,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
		stopCh:     make(chan struct{}),
	}
	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		logger.Error().Err(err).Msg("discord: failed to create session")
		return t
	}
	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
	session.AddHandler(t.onMessageCreate)
	session.AddHandler(t.onInteractionCreate)
	t.session = session
	return t
}

// Name returns the transport identifier.
func (t *DiscordTransport) Name() string { return "discord" }

// ResolveChannelName returns the Discord channel name for the given channel ID.
func (t *DiscordTransport) ResolveChannelName(_ context.Context, channelID string) (string, error) {
	if t.session == nil {
		return "", nil
	}
	ch, err := t.session.Channel(channelID)
	if err != nil {
		return "", nil // not found on this transport
	}
	return ch.Name, nil
}

// OnMessage registers the inbound message handler. Must be called before Start.
func (t *DiscordTransport) OnMessage(handler func(ctx context.Context, msg InboundMessage)) {
	t.handler = handler
}

// Start connects to the Discord Gateway and blocks until ctx is cancelled.
func (t *DiscordTransport) Start(ctx context.Context) error {
	if t.session == nil {
		return fmt.Errorf("discord: session not initialized")
	}
	if err := t.session.Open(); err != nil {
		return fmt.Errorf("discord: open gateway: %w", err)
	}
	t.logger.Info().Msg("discord: gateway connected")
	t.registerSlashCommands(ctx)

	select {
	case <-ctx.Done():
	case <-t.stopCh:
	}
	return ctx.Err()
}

// Stop gracefully shuts down the transport.
func (t *DiscordTransport) Stop(_ context.Context) error {
	select {
	case <-t.stopCh:
	default:
		close(t.stopCh)
	}
	if t.session != nil {
		return t.session.Close()
	}
	return nil
}

func (t *DiscordTransport) onMessageCreate(_ *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot || t.handler == nil {
		return
	}
	msg := InboundMessage{
		ID:        m.ID,
		ChannelID: m.ChannelID,
		UserID:    m.Author.ID,
		Username:  m.Author.Username,
		Text:      m.Content,
		Platform:  "discord",
	}
	if m.ReferencedMessage != nil {
		msg.ReplyToID = m.ReferencedMessage.ID
	}
	go t.handler(context.Background(), msg)
}

func (t *DiscordTransport) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()

	// ACK immediately with an ephemeral placeholder.
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "...",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	text := "/" + data.Name
	for _, opt := range data.Options {
		text += " " + opt.StringValue()
	}

	var userID, username string
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
		username = i.Member.User.Username
	} else if i.User != nil {
		userID = i.User.ID
		username = i.User.Username
	}

	if t.handler != nil {
		go t.handler(context.Background(), InboundMessage{
			ID:        i.ID,
			ChannelID: i.ChannelID,
			UserID:    userID,
			Username:  username,
			Text:      text,
			Platform:  "discord",
		})
	}
}

// registerSlashCommands registers global slash commands with Discord (idempotent PUT).
func (t *DiscordTransport) registerSlashCommands(ctx context.Context) {
	if t.appID == "" {
		t.logger.Warn().Msg("discord: no app ID configured, skipping slash command registration")
		return
	}
	commands := []map[string]any{
		{
			"name":        "default_role",
			"description": "Set the default person or tag that responds in this channel",
			"options": []map[string]any{
				{"name": "role", "description": "Person username, tag name, or 'none' to clear", "type": 3, "required": false},
			},
		},
		{"name": "chat", "description": "Switch to chat mode (no tools — faster & cheaper)"},
		{"name": "execute", "description": "Switch to execute mode (full tools enabled)"},
		{
			"name":        "mode",
			"description": "Show or switch the current mode",
			"options": []map[string]any{
				{"name": "name", "description": "Mode name to switch to (omit to show current)", "type": 3, "required": false},
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
