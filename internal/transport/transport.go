// Package transport defines the platform-agnostic message transport interface
// and normalized message types used by the agent core.
package transport

import "context"

// InboundMessage is a normalized inbound message from any platform.
type InboundMessage struct {
	ID        string
	ChannelID string
	UserID    string
	Username  string
	Text      string
	ReplyToID string
	Platform  string // "http", "telegram", "discord", "slack"
}

// OutboundMessage is a normalized outbound message to any platform.
type OutboundMessage struct {
	ChannelID string
	ReplyToID string
	Text      string
	Markdown  bool
}

// Transport is the interface all platform adapters must implement.
type Transport interface {
	// Start begins accepting messages. Blocks until ctx is cancelled or an error occurs.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the transport.
	Stop(ctx context.Context) error

	// Send delivers an outbound message.
	Send(ctx context.Context, msg OutboundMessage) error

	// OnMessage registers the handler called for every inbound message.
	// Must be called before Start.
	OnMessage(handler func(ctx context.Context, msg InboundMessage))

	// Name returns the transport identifier (e.g., "http", "telegram").
	Name() string
}
