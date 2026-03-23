// Package api provides the REST API server for the Capabot web UI.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/cron"
	applog "github.com/polymath/capabot/internal/log"
	"github.com/polymath/capabot/internal/llm"
	"github.com/polymath/capabot/internal/memory"
	"github.com/polymath/capabot/internal/orchestrator"
	"github.com/polymath/capabot/internal/skill"
	"github.com/rs/zerolog"
)

var startTime = time.Now()

// Server is the REST API server for the Capabot web UI.
type Server struct {
	store          *memory.Store
	skillReg       *skill.Registry
	agentReg       *orchestrator.Registry
	providers      map[string]llm.Provider
	toolReg        *agent.Registry
	defaultAgent   func(ctx context.Context, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error)
	logBroadcaster *applog.Broadcaster
	scheduler      *cron.Scheduler
	mux            *http.ServeMux
	handler        http.Handler // mux wrapped with middleware
	logger         zerolog.Logger
	skillsDir      string // destination for skills installed via API
	clawHubToken   string // optional GitHub PAT for ClawHub requests
	configPath     string // path to config.yaml for key management
	router         *llm.Router
}

// Config holds dependencies for the API server.
type Config struct {
	Store          *memory.Store
	SkillReg       *skill.Registry
	AgentReg       *orchestrator.Registry
	Providers      map[string]llm.Provider
	ToolReg        *agent.Registry
	DefaultAgent   func(ctx context.Context, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error)
	LogBroadcaster *applog.Broadcaster
	Logger         zerolog.Logger
	// StaticFS is the embedded web/dist for SPA serving (nil = skip static serving).
	StaticFS fs.FS
	// APIKey is an optional bearer token. Empty = no auth required.
	APIKey string
	// RateLimitRPM limits API requests per minute per IP. 0 = disabled.
	RateLimitRPM int
	// SkillsDir is the directory where skills installed via the API are written.
	// Defaults to the user's ~/.capabot/skills if empty.
	SkillsDir string
	// ClawHubToken is an optional GitHub PAT to raise ClawHub API rate limits.
	ClawHubToken string
	// Scheduler is the cron scheduler for automations.
	Scheduler *cron.Scheduler
	// ConfigPath is the path to config.yaml for API key management.
	ConfigPath string
	// Router is the LLM router, used to hot-reload provider keys.
	Router *llm.Router
}

// New creates a new API server and registers all routes.
func New(cfg Config) *Server {
	skillsDir := cfg.SkillsDir
	if skillsDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			skillsDir = home + "/.capabot/skills"
		}
	}

	s := &Server{
		store:          cfg.Store,
		skillReg:       cfg.SkillReg,
		agentReg:       cfg.AgentReg,
		providers:      cfg.Providers,
		toolReg:        cfg.ToolReg,
		defaultAgent:   cfg.DefaultAgent,
		logBroadcaster: cfg.LogBroadcaster,
		scheduler:      cfg.Scheduler,
		mux:            http.NewServeMux(),
		logger:         cfg.Logger,
		skillsDir:      skillsDir,
		clawHubToken:   cfg.ClawHubToken,
		configPath:     cfg.ConfigPath,
		router:         cfg.Router,
	}

	// REST endpoints
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/agents", s.handleAgents)
	s.mux.HandleFunc("GET /api/conversations", s.handleConversations)
	s.mux.HandleFunc("GET /api/conversations/{id}", s.handleConversation)
	s.mux.HandleFunc("GET /api/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/skills/catalog", s.handleSkillsCatalog)
	s.mux.HandleFunc("POST /api/skills/install", s.handleSkillsInstall)
	s.mux.HandleFunc("POST /api/skills/create", s.handleSkillsCreate)
	s.mux.HandleFunc("GET /api/skills/{name}", s.handleSkillGet)
	s.mux.HandleFunc("PUT /api/skills/{name}", s.handleSkillUpdate)
	s.mux.HandleFunc("DELETE /api/skills/{name}", s.handleSkillsUninstall)
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
	s.mux.HandleFunc("GET /api/config/keys", s.handleConfigKeysGet)
	s.mux.HandleFunc("PUT /api/config/keys", s.handleConfigKeysPut)

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

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if s.agentReg == nil {
		writeJSON(w, []any{})
		return
	}
	cfgs := s.agentReg.List()
	type agentDTO struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Provider    string   `json:"provider"`
		Model       string   `json:"model"`
		Skills      []string `json:"skills"`
		Tools       []string `json:"tools"`
		MaxTokens   int      `json:"max_tokens"`
		Temperature float64  `json:"temperature"`
	}
	out := make([]agentDTO, len(cfgs))
	for i, c := range cfgs {
		out[i] = agentDTO{
			ID:          c.ID,
			Name:        c.Name,
			Provider:    c.Provider,
			Model:       c.Model,
			Skills:      c.Skills,
			Tools:       c.Tools,
			MaxTokens:   c.MaxTokens,
			Temperature: c.Temperature,
		}
	}
	writeJSON(w, out)
}

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, []any{})
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 {
		limit = 50
	}
	offsetStr := r.URL.Query().Get("offset")
	offset, _ := strconv.Atoi(offsetStr)

	tenantID := TenantIDFromContext(r.Context())
	sessions, err := s.store.ListSessions(r.Context(), tenantID, limit, offset)
	if err != nil {
		writeError(w, fmt.Sprintf("listing conversations: %v", err), http.StatusInternalServerError)
		return
	}

	type sessionDTO struct {
		ID           string    `json:"id"`
		Channel      string    `json:"channel"`
		UserID       string    `json:"user_id"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
		MessageCount int       `json:"message_count"`
	}
	out := make([]sessionDTO, len(sessions))
	for i, sess := range sessions {
		count, _ := s.store.CountMessages(r.Context(), sess.ID)
		out[i] = sessionDTO{
			ID:           sess.ID,
			Channel:      sess.Channel,
			UserID:       sess.UserID,
			CreatedAt:    sess.CreatedAt,
			UpdatedAt:    sess.UpdatedAt,
			MessageCount: count,
		}
	}
	writeJSON(w, out)
}

func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, "store not available", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	tenantID := TenantIDFromContext(r.Context())
	sess, err := s.store.GetSession(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, fmt.Sprintf("session not found: %v", err), http.StatusNotFound)
		return
	}
	msgs, err := s.store.GetMessages(r.Context(), id)
	if err != nil {
		writeError(w, fmt.Sprintf("getting messages: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"session":  sess,
		"messages": msgs,
	})
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if s.skillReg == nil {
		writeJSON(w, []any{})
		return
	}
	all := s.skillReg.List()
	type skillDTO struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Version      string `json:"version"`
		Instructions string `json:"instructions"`
		Removable    bool   `json:"removable"`
		Tier         int    `json:"tier"` // 1=prompt-only, 2=native Go, 3=WASM
	}
	out := make([]skillDTO, len(all))
	for i, sk := range all {
		name := sk.Manifest.Name
		path, hasPath := s.skillReg.SkillPath(name)
		removable := hasPath && strings.HasPrefix(path, s.skillsDir)
		tier := 1
		if _, ok := s.skillReg.NativePath(name); ok {
			tier = 2
		} else if _, ok := s.skillReg.WASMPath(name); ok {
			tier = 3
		}
		out[i] = skillDTO{
			Name:         name,
			Description:  sk.Manifest.Description,
			Version:      sk.Manifest.Version,
			Instructions: sk.Instructions,
			Removable:    removable,
			Tier:         tier,
		}
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

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages  []llm.ChatMessage `json:"messages"`
		SessionID string            `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, "messages is required", http.StatusBadRequest)
		return
	}
	if s.defaultAgent == nil {
		writeError(w, "no agent configured", http.StatusServiceUnavailable)
		return
	}

	lastUserText := lastUserContent(req.Messages)
	tenantID := TenantIDFromContext(r.Context())
	sessionID := s.ensureSession(r.Context(), req.SessionID, tenantID, lastUserText)

	result, err := s.defaultAgent(r.Context(), sessionID, req.Messages, nil)
	if err != nil {
		writeError(w, fmt.Sprintf("agent error: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"session_id":  sessionID,
		"response":    result.Response,
		"tool_calls":  result.ToolCalls,
		"iterations":  result.Iterations,
		"usage":       result.Usage,
		"stop_reason": result.StopReason,
	})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages  []llm.ChatMessage `json:"messages"`
		SessionID string            `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, "messages is required", http.StatusBadRequest)
		return
	}
	if s.defaultAgent == nil {
		writeError(w, "no agent configured", http.StatusServiceUnavailable)
		return
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// eventCh carries agent events to the SSE writer goroutine.
	// Buffer 64 so the agent never blocks emitting events.
	eventCh := make(chan agent.AgentEvent, 64)
	doneCh := make(chan struct{})

	// Writer goroutine: drains eventCh and sends SSE frames.
	go func() {
		defer close(doneCh)
		for ev := range eventCh {
			sendSSE(w, flusher, map[string]any{
				"event":      string(ev.Kind),
				"tool_name":  ev.ToolName,
				"tool_id":    ev.ToolID,
				"tool_input": ev.ToolInput,
				"content":    ev.Content,
				"thinking":   ev.Thinking,
				"is_error":   ev.IsError,
				"iteration":  ev.Iteration,
			})
		}
	}()

	lastUserText := lastUserContent(req.Messages)
	tenantID := TenantIDFromContext(r.Context())
	sessionID := s.ensureSession(r.Context(), req.SessionID, tenantID, lastUserText)

	sendSSE(w, flusher, map[string]any{"session_id": sessionID})
	result, err := s.defaultAgent(r.Context(), sessionID, req.Messages, func(ev agent.AgentEvent) {
		select {
		case eventCh <- ev:
		default: // drop if buffer full rather than blocking agent
		}
	})
	close(eventCh)
	<-doneCh // wait for writer to flush

	if err != nil {
		sendSSE(w, flusher, map[string]any{"error": err.Error(), "done": true})
		return
	}
	sendSSE(w, flusher, map[string]any{
		"done":       true,
		"tool_calls": result.ToolCalls,
		"iterations": result.Iterations,
		"usage":      result.Usage,
	})
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

func (s *Server) handleSkillsCatalog(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 10000
	}

	client := skill.NewClawHubClient(skill.ClawHubConfig{})
	results, err := client.BrowseSkills(r.Context(), query, limit, offset)
	if err != nil {
		writeError(w, fmt.Sprintf("ClawHub error: %v", err), http.StatusBadGateway)
		return
	}
	if results == nil {
		results = []skill.ClawHubSkillEntry{}
	}
	writeJSON(w, results)
}

func (s *Server) handleSkillsInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}

	client := skill.NewClawHubClient(skill.ClawHubConfig{})

	tmpDir, err := os.MkdirTemp("", "capabot-install-*")
	if err != nil {
		writeError(w, fmt.Sprintf("temp dir: %v", err), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	skillPath, err := client.DownloadSkill(r.Context(), req.Name, tmpDir)
	if err != nil {
		writeError(w, fmt.Sprintf("download failed: %v", err), http.StatusBadGateway)
		return
	}

	result, err := skill.ImportSkill(skillPath, s.skillsDir)
	if err != nil {
		writeError(w, fmt.Sprintf("import failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Hot-reload the skill registry so the agent can use the new skill immediately.
	if s.skillReg != nil {
		s.skillReg.LoadDir(s.skillsDir) //nolint:errcheck
	}

	writeJSON(w, map[string]any{
		"skill_name": result.SkillName,
		"tier":       result.Tier,
		"success":    result.Success,
		"warnings":   result.Warnings,
	})
}

func (s *Server) handleSkillsUninstall(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if s.skillReg == nil {
		writeError(w, "skill registry not available", http.StatusServiceUnavailable)
		return
	}

	skillPath, ok := s.skillReg.SkillPath(name)
	if !ok {
		writeError(w, fmt.Sprintf("skill %q not found", name), http.StatusNotFound)
		return
	}

	// Only allow removing skills that live inside the API-managed skills dir.
	if !strings.HasPrefix(skillPath, s.skillsDir) {
		writeError(w, "skill is not removable (system or workspace skill)", http.StatusForbidden)
		return
	}

	if err := os.RemoveAll(skillPath); err != nil {
		writeError(w, fmt.Sprintf("removing skill: %v", err), http.StatusInternalServerError)
		return
	}

	s.skillReg.Unregister(name)
	writeJSON(w, map[string]any{"success": true, "name": name})
}

// lastUserContent returns the content of the last user message in a slice.
func lastUserContent(messages []llm.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// newSessionID generates a random hex session ID.
func newSessionID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// ensureSession upserts a session and saves the user message. Returns the session ID.
func (s *Server) ensureSession(ctx context.Context, sessionID, tenantID, text string) string {
	if sessionID == "" {
		sessionID = newSessionID()
	}
	if s.store != nil {
		_ = s.store.UpsertSession(ctx, memory.Session{
			ID:       sessionID,
			TenantID: tenantID,
			Channel:  "web",
		})
		_, _ = s.store.SaveMessage(ctx, memory.Message{
			SessionID: sessionID,
			Role:      "user",
			Content:   text,
		})
	}
	return sessionID
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
