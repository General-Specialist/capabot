package log

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// New creates a zerolog.Logger configured for the given level.
// When pretty is true, output is human-readable console format;
// otherwise JSON structured logs for production.
func New(level string, pretty bool) zerolog.Logger {
	lvl := parseLevel(level)

	var w io.Writer = os.Stderr
	if pretty {
		w = zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
		}
	}

	return zerolog.New(w).
		Level(lvl).
		With().
		Timestamp().
		Logger()
}

// WithContext returns a child logger enriched with tenant, session, and agent IDs.
func WithContext(logger zerolog.Logger, tenantID, sessionID, agentID string) zerolog.Logger {
	ctx := logger.With()
	if tenantID != "" {
		ctx = ctx.Str("tenant_id", tenantID)
	}
	if sessionID != "" {
		ctx = ctx.Str("session_id", sessionID)
	}
	if agentID != "" {
		ctx = ctx.Str("agent_id", agentID)
	}
	return ctx.Logger()
}

func parseLevel(level string) zerolog.Level {
	switch level {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}
