// Package api provides the REST API server for the GoStaff web UI.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/cron"
	applog "github.com/polymath/gostaff/internal/log"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/memory"
	"github.com/polymath/gostaff/internal/skill"
	"github.com/polymath/gostaff/internal/transport"
	"github.com/rs/zerolog"
)

var startTime = time.Now()

// Server is the REST API server for the GoStaff web UI.
type Server struct {
	store          *memory.Store
	skillReg       *skill.Registry
	providers      map[string]llm.Provider
	toolReg        *agent.Registry
	runAgent func(ctx context.Context, sysPrompt, model, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error)
	logBroadcaster *applog.Broadcaster
	scheduler      *cron.Scheduler
	mux            *http.ServeMux
	handler        http.Handler // mux wrapped with middleware
	logger         zerolog.Logger
	skillsDir      string // destination for skills installed via API
	clawHubToken   string // optional GitHub PAT for ClawHub requests
	configPath     string // path to config.yaml for key management
	router           *llm.Router
	discordRoles     *transport.DiscordRoleClient
	channelResolvers []transport.ChannelNameResolver
}

// Config holds dependencies for the API server.
type Config struct {
	Store          *memory.Store
	SkillReg       *skill.Registry
	Providers      map[string]llm.Provider
	ToolReg        *agent.Registry
	RunAgent func(ctx context.Context, sysPrompt, model, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error)
	LogBroadcaster  *applog.Broadcaster
	Logger         zerolog.Logger
	// StaticFS is the embedded web/dist for SPA serving (nil = skip static serving).
	StaticFS fs.FS
	// APIKey is an optional bearer token. Empty = no auth required.
	APIKey string
	// RateLimitRPM limits API requests per minute per IP. 0 = disabled.
	RateLimitRPM int
	// SkillsDir is the directory where skills installed via the API are written.
	// Defaults to the user's ~/.gostaff/skills if empty.
	SkillsDir string
	// ClawHubToken is an optional GitHub PAT to raise ClawHub API rate limits.
	ClawHubToken string
	// Scheduler is the cron scheduler for automations.
	Scheduler *cron.Scheduler
	// ConfigPath is the path to config.yaml for API key management.
	ConfigPath string
	// Router is the LLM router, used to hot-reload provider keys.
	Router *llm.Router
	// DiscordRoles syncs person roles to Discord (nil if Discord not configured).
	DiscordRoles *transport.DiscordRoleClient
	// ChannelResolvers resolves channel IDs to human-readable names across transports.
	ChannelResolvers []transport.ChannelNameResolver
}

// New creates a new API server and registers all routes.
func New(cfg Config) *Server {
	skillsDir := cfg.SkillsDir
	if skillsDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			skillsDir = home + "/.gostaff/skills"
		}
	}

	s := &Server{
		store:           cfg.Store,
		skillReg:        cfg.SkillReg,
		providers:       cfg.Providers,
		toolReg:         cfg.ToolReg,
		runAgent: cfg.RunAgent,
		logBroadcaster:  cfg.LogBroadcaster,
		scheduler:      cfg.Scheduler,
		mux:            http.NewServeMux(),
		logger:         cfg.Logger,
		skillsDir:      skillsDir,
		clawHubToken:   cfg.ClawHubToken,
		configPath:     cfg.ConfigPath,
		router:           cfg.Router,
		discordRoles:     cfg.DiscordRoles,
		channelResolvers: cfg.ChannelResolvers,
	}

	// REST endpoints
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/conversations", s.handleConversations)
	s.mux.HandleFunc("GET /api/conversations/{id}", s.handleConversation)
	s.mux.HandleFunc("GET /api/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/skills/catalog", s.handleSkillsCatalog)
	s.mux.HandleFunc("POST /api/skills/install", s.handleSkillsInstall)
	s.mux.HandleFunc("POST /api/skills/create", s.handleSkillsCreate)
	s.mux.HandleFunc("POST /api/skills/create-markdown", s.handleSkillsCreateMarkdown)
	s.mux.HandleFunc("GET /api/skills/{name}", s.handleSkillGet)
	s.mux.HandleFunc("PUT /api/skills/{name}", s.handleSkillUpdate)
	s.mux.HandleFunc("DELETE /api/skills/{name}", s.handleSkillsUninstall)
	s.mux.HandleFunc("GET /api/skills/{name}/config", s.handleSkillConfigGet)
	s.mux.HandleFunc("PUT /api/skills/{name}/config", s.handleSkillConfigSet)
	s.mux.HandleFunc("GET /api/channels/{id}/resolve", s.handleChannelResolve)
	s.mux.HandleFunc("GET /api/tools", s.handleToolsList)
	s.mux.HandleFunc("GET /api/channels", s.handleChannelsList)
	s.mux.HandleFunc("GET /api/channels/{id}", s.handleChannelGet)
	s.mux.HandleFunc("PUT /api/channels/{id}", s.handleChannelSet)
	s.mux.HandleFunc("DELETE /api/channels/{id}", s.handleChannelDelete)
	s.mux.HandleFunc("GET /api/providers", s.handleProviders)
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	s.mux.HandleFunc("GET /api/logs", s.handleLogs)
	s.mux.HandleFunc("GET /api/automations", s.handleAutomationsList)
	s.mux.HandleFunc("POST /api/automations", s.handleAutomationsCreate)
	s.mux.HandleFunc("GET /api/automations/{id}", s.handleAutomationsGet)
	s.mux.HandleFunc("PUT /api/automations/{id}", s.handleAutomationsUpdate)
	s.mux.HandleFunc("DELETE /api/automations/{id}", s.handleAutomationsDelete)
	s.mux.HandleFunc("POST /api/automations/{id}/trigger", s.handleAutomationsTrigger)
	s.mux.HandleFunc("GET /api/automations/{id}/runs", s.handleAutomationsRuns)
	s.mux.HandleFunc("GET /api/runs/{automationID}/{runID}/trace", s.handleRunTrace)
	s.mux.HandleFunc("POST /api/runs/{runID}/stop", s.handleRunStop)
	s.mux.HandleFunc("GET /api/runs/{runID}/stream", s.handleRunStream)
	s.mux.HandleFunc("GET /api/runs", s.handleAllRuns)
	s.mux.HandleFunc("GET /api/config/keys", s.handleConfigKeysGet)
	s.mux.HandleFunc("PUT /api/config/keys", s.handleConfigKeysPut)
	s.mux.HandleFunc("GET /api/config/transport-keys", s.handleTransportKeysGet)
	s.mux.HandleFunc("PUT /api/config/transport-keys", s.handleTransportKeysPut)
	s.mux.HandleFunc("GET /api/people", s.handlePeopleList)
	s.mux.HandleFunc("POST /api/people", s.handlePeopleCreate)
	s.mux.HandleFunc("GET /api/people/system-prompt", s.handleSystemPromptGet)
	s.mux.HandleFunc("PUT /api/people/system-prompt", s.handleSystemPromptPut)
	s.mux.HandleFunc("GET /api/settings/execute-fallback", s.handleExecuteFallbackGet)
	s.mux.HandleFunc("PUT /api/settings/execute-fallback", s.handleExecuteFallbackPut)
	s.mux.HandleFunc("GET /api/settings/shell-mode", s.handleShellModeGet)
	s.mux.HandleFunc("PUT /api/settings/shell-mode", s.handleShellModePut)
	s.mux.HandleFunc("GET /api/settings/shell-approved", s.handleShellApprovedGet)
	s.mux.HandleFunc("PUT /api/settings/shell-approved", s.handleShellApprovedPut)
	s.mux.HandleFunc("GET /api/usage", s.handleUsage)
	s.mux.HandleFunc("GET /api/credits", s.handleCredits)
	s.mux.HandleFunc("GET /api/modes", s.handleModesGet)
	s.mux.HandleFunc("PUT /api/modes/active", s.handleActiveModePut)
	s.mux.HandleFunc("PUT /api/modes/{name}", s.handleModesPut)
	s.mux.HandleFunc("DELETE /api/modes/{name}", s.handleModesDelete)
	s.mux.HandleFunc("PUT /api/people/{id}", s.handlePeopleUpdate)
	s.mux.HandleFunc("DELETE /api/people/{id}", s.handlePeopleDelete)
	s.mux.HandleFunc("GET /api/memory", s.handleMemoryList)
	s.mux.HandleFunc("PUT /api/memory/{key}", s.handleMemoryUpsert)
	s.mux.HandleFunc("DELETE /api/memory/{key}", s.handleMemoryDelete)
	s.mux.HandleFunc("POST /api/avatars", s.handleAvatarUpload)
	s.mux.Handle("GET /api/avatars/", http.StripPrefix("/api/avatars/", http.FileServer(http.Dir(s.avatarsDir()))))

	// SPA static files
	if cfg.StaticFS != nil {
		s.mux.Handle("/", spaHandler(cfg.StaticFS))
	}

	// Wrap mux with middleware (outermost first):
	// tenant → rate limit → auth → mux
	var h http.Handler = s.mux
	h = authMiddleware(cfg.APIKey, h)
	h = rateLimitMiddleware(cfg.RateLimitRPM, h)
	h = tenantMiddleware(h)
	s.handler = h

	return s
}

// Handler returns the http.Handler wrapped with auth and rate-limiting middleware.
func (s *Server) Handler() http.Handler { return s.handler }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// --- handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	skillsLoaded := 0
	if s.skillReg != nil {
		skillsLoaded = s.skillReg.Len()
	}
	writeJSON(w, map[string]any{
		"status":          "ok",
		"version":         "0.1.0",
		"uptime_seconds":  int(time.Since(startTime).Seconds()),
		"skills_loaded":   skillsLoaded,
		"providers_count": len(s.providers),
	})
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if s.skillReg == nil {
		writeJSON(w, []any{})
		return
	}
	all := s.skillReg.List()
	type skillDTO struct {
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		Version      string          `json:"version"`
		Instructions string          `json:"instructions"`
		Removable    bool            `json:"removable"`
		Tier         int             `json:"tier"`    // 1=prompt-only, 2=native Go, 3=plugin (TS/JS/Python)
		Source       string          `json:"source"` // "custom" or "clawhub"
		ConfigSchema json.RawMessage `json:"config_schema,omitempty"`
		HasConfig    bool            `json:"has_config"`
	}
	out := make([]skillDTO, len(all))
	for i, sk := range all {
		name := sk.Manifest.Name
		path, hasPath := s.skillReg.SkillPath(name)
		removable := hasPath && strings.HasPrefix(path, s.skillsDir)
		tier := 1
		if _, ok := s.skillReg.NativePath(name); ok {
			tier = 2
		} else if _, ok := s.skillReg.PluginPath(name); ok {
			tier = 3
		}
		source := "custom"
		if hasPath {
			if _, err := os.Stat(filepath.Join(path, "_meta.json")); err == nil {
				source = "clawhub"
			}
		}
		// Load config schema and check for config.json (tier 3 plugins only).
		var configSchema json.RawMessage
		hasConfig := false
		if tier == 3 && hasPath {
			if data, err := os.ReadFile(filepath.Join(path, "_config_schema.json")); err == nil {
				configSchema = data
			}
			if _, err := os.Stat(filepath.Join(path, "config.json")); err == nil {
				hasConfig = true
			}
		}

		out[i] = skillDTO{
			Name:         name,
			Description:  sk.Manifest.Description,
			Version:      sk.Manifest.Version,
			Instructions: strings.TrimSpace(sk.Instructions),
			Removable:    removable,
			Tier:         tier,
			Source:       source,
			ConfigSchema: configSchema,
			HasConfig:    hasConfig,
		}
	}
	writeJSON(w, out)
}

func (s *Server) handleChannelResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, resolver := range s.channelResolvers {
		name, err := resolver.ResolveChannelName(r.Context(), id)
		if err == nil && name != "" {
			writeJSON(w, map[string]string{"name": name})
			return
		}
	}
	writeError(w, "channel not found", http.StatusNotFound)
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request) {
	if s.toolReg == nil {
		writeJSON(w, []any{})
		return
	}
	type toolDTO struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	tools := s.toolReg.List()
	out := make([]toolDTO, len(tools))
	for i, t := range tools {
		out[i] = toolDTO{Name: t.Name(), Description: t.Description()}
	}
	writeJSON(w, out)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	type modelDTO struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextWindow int    `json:"context_window"`
	}
	type providerDTO struct {
		Name   string     `json:"name"`
		Models []modelDTO `json:"models"`
	}
	out := make([]providerDTO, 0, len(s.providers))
	for name, p := range s.providers {
		models := p.Models()
		mDTOs := make([]modelDTO, len(models))
		for i, m := range models {
			mDTOs[i] = modelDTO{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow}
		}
		out = append(out, providerDTO{Name: name, Models: mDTOs})
	}
	writeJSON(w, out)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.logBroadcaster == nil {
		writeError(w, "log streaming not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Replay recent log entries to the new subscriber.
	for _, line := range s.logBroadcaster.Recent(100) {
		sendSSE(w, flusher, map[string]any{"line": line})
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	ch := s.logBroadcaster.Subscribe(ctx)
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return
			}
			sendSSE(w, flusher, map[string]any{"line": line})
		case <-r.Context().Done():
			return
		}
	}
}

// --- helpers ---

func sendSSE(w http.ResponseWriter, flusher http.Flusher, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// spaHandler serves an embedded SPA: API calls pass through, everything else
// serves index.html (for client-side routing).
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(fsys, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for client-side routing
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r2)
	})
}
