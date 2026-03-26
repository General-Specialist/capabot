// Package api provides the REST API server for the GoStaff web UI.
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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	router         *llm.Router
	discordRoles   *transport.DiscordRoleClient
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
		router:         cfg.Router,
		discordRoles:   cfg.DiscordRoles,
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
		Title        string    `json:"title"`
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
			Title:        sess.Title,
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
		Tier         int    `json:"tier"`    // 1=prompt-only, 2=native Go, 3=plugin (TS/JS/Python)
		Source       string `json:"source"` // "custom" or "clawhub"
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
		out[i] = skillDTO{
			Name:         name,
			Description:  sk.Manifest.Description,
			Version:      sk.Manifest.Version,
			Instructions: strings.TrimSpace(sk.Instructions),
			Removable:    removable,
			Tier:         tier,
			Source:       source,
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

// preparedChat holds the resolved state shared by handleChat and handleChatStream.
type preparedChat struct {
	sessionID string
	msgs      []llm.ChatMessage
	sysPrompt string
	modelID   string
	people    []memory.Person
}

// prepareChatRequest resolves session, model tag, global system prompt, and persona mentions.
func (s *Server) prepareChatRequest(ctx context.Context, messages []llm.ChatMessage, sessionID, tenantID string) preparedChat {
	lastUserText := lastUserContent(messages)
	sid := s.ensureSession(ctx, sessionID, tenantID, lastUserText)

	var globalSysPrompt string
	if s.store != nil {
		globalSysPrompt, _ = s.store.GetSystemPrompt(ctx)
	}

	modelID := s.extractModelTag(lastUserText)
	if modelID == "" && s.store != nil {
		modelID, _ = s.store.GetSetting(ctx, "default_model")
	} else if modelID != "" {
		lastUserText = strings.TrimSpace(strings.Replace(lastUserText, "@"+modelID, "", 1))
	}

	strippedText, people := s.resolvePeople(ctx, lastUserText)

	msgs := messages
	if strippedText != lastUserContent(messages) {
		msgs = make([]llm.ChatMessage, len(messages))
		copy(msgs, messages)
		msgs[len(msgs)-1] = llm.ChatMessage{Role: "user", Content: strippedText}
	}

	return preparedChat{
		sessionID: sid,
		msgs:      msgs,
		sysPrompt: globalSysPrompt,
		modelID:   modelID,
		people:    people,
	}
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
	if s.runAgent == nil {
		writeError(w, "no agent configured", http.StatusServiceUnavailable)
		return
	}

	tenantID := TenantIDFromContext(r.Context())
	p := s.prepareChatRequest(r.Context(), req.Messages, req.SessionID, tenantID)

	// Apply single person prompt if present (multi-person not supported in sync path).
	sysPrompt := p.sysPrompt
	if len(p.people) == 1 {
		sysPrompt = combinePrompts(p.sysPrompt, p.people[0].Prompt)
	}

	result, err := s.runAgent(r.Context(), sysPrompt, p.modelID, p.sessionID, p.msgs, nil)
	if err != nil {
		writeError(w, fmt.Sprintf("agent error: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"session_id":  p.sessionID,
		"response":    result.Response,
		"tool_calls":  result.ToolCalls,
		"iterations":  result.Iterations,
		"usage":       result.Usage,
		"stop_reason": result.StopReason,
	})
}

// resolvePeople checks if text starts with @username, @tag, or a Discord role mention <@&ID>.
// Returns the stripped text and matching people.
func (s *Server) resolvePeople(ctx context.Context, text string) (string, []memory.Person) {
	if s.store == nil || len(text) < 2 {
		return text, nil
	}

	// Check for Discord role mention: <@&ROLE_ID>
	if strings.HasPrefix(text, "<@&") {
		end := strings.Index(text, ">")
		if end > 3 {
			roleID := text[3:end]
			remainder := strings.TrimLeft(text[end+1:], " ")
			if remainder == "" {
				remainder = text
			}
			// Try person role.
			person, err := s.store.GetPersonByDiscordRoleID(ctx, roleID)
			if err == nil {
				return remainder, []memory.Person{person}
			}
			// Try tag role.
			tag, err := s.store.GetTagByDiscordRoleID(ctx, roleID)
			if err == nil {
				tagged, err := s.store.GetPeopleByTag(ctx, tag)
				if err == nil && len(tagged) > 0 {
					return remainder, tagged
				}
			}
		}
	}

	if text[0] != '@' {
		return text, nil
	}
	rest := text[1:]
	name := rest
	remainder := ""
	for i, c := range rest {
		if c == ' ' || c == '\n' {
			name = rest[:i]
			remainder = rest[i+1:]
			break
		}
	}
	if name == "" {
		return text, nil
	}
	if remainder == "" {
		remainder = text
	}

	// Try exact username first (the @mention handle).
	person, err := s.store.GetPersonByUsername(ctx, name)
	if err == nil {
		return remainder, []memory.Person{person}
	}

	// Try as a tag.
	tagged, err := s.store.GetPeopleByTag(ctx, name)
	if err == nil && len(tagged) > 0 {
		return remainder, tagged
	}

	return text, nil
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
	if s.runAgent == nil {
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

	tenantID := TenantIDFromContext(r.Context())
	p := s.prepareChatRequest(r.Context(), req.Messages, req.SessionID, tenantID)
	sendSSE(w, flusher, map[string]any{"session_id": p.sessionID})

	if len(p.people) == 0 {
		s.streamSingleAgent(r.Context(), w, flusher, p.sessionID, p.msgs, p.sysPrompt, p.modelID, "")
		return
	}

	if len(p.people) == 1 {
		person := p.people[0]
		displayName := person.Username
		if displayName == "" {
			displayName = person.Name
		}
		s.streamSingleAgent(r.Context(), w, flusher, p.sessionID, p.msgs, combinePrompts(p.sysPrompt, person.Prompt), p.modelID, displayName)
		return
	}

	// Multiple people — fan out in parallel, prepend global system prompt to each.
	people := p.people
	if p.sysPrompt != "" {
		enriched := make([]memory.Person, len(people))
		for i, person := range people {
			enriched[i] = person
			enriched[i].Prompt = combinePrompts(p.sysPrompt, person.Prompt)
		}
		people = enriched
	}
	s.streamMultiAgent(r.Context(), w, flusher, p.sessionID, p.msgs, people)
}

// streamSingleAgent runs one agent and streams its events.
// If sysPrompt is empty, uses the default agent. model overrides the LLM model if set.
func (s *Server) streamSingleAgent(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, sessionID string, messages []llm.ChatMessage, sysPrompt, model, personaName string) {
	eventCh := make(chan agent.AgentEvent, 64)
	doneCh := make(chan struct{})

	go func() {
		defer close(doneCh)
		for ev := range eventCh {
			payload := map[string]any{
				"event":      string(ev.Kind),
				"tool_name":  ev.ToolName,
				"tool_id":    ev.ToolID,
				"tool_input": ev.ToolInput,
				"content":    ev.Content,
				"thinking":   ev.Thinking,
				"is_error":   ev.IsError,
				"iteration":  ev.Iteration,
			}
			if personaName != "" {
				payload["persona"] = personaName
			}
			sendSSE(w, flusher, payload)
		}
	}()

	onEvent := func(ev agent.AgentEvent) {
		select {
		case eventCh <- ev:
		default:
		}
	}

	result, err := s.runAgent(ctx, sysPrompt, model, sessionID, messages, onEvent)
	close(eventCh)
	<-doneCh

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

// personEvent is a tagged agent event for multiplexing parallel person streams.
type personEvent struct {
	person memory.Person
	event  agent.AgentEvent
}

// personDone signals that a person's agent run has completed.
type personDone struct {
	person memory.Person
	result *agent.RunResult
	err    error
}

// streamMultiAgent runs multiple people in parallel, all seeing the full chat history.
// Events are multiplexed onto the single SSE connection with a "persona" field.
func (s *Server) streamMultiAgent(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, sessionID string, messages []llm.ChatMessage, people []memory.Person) {
	eventCh := make(chan personEvent, 64*len(people))
	doneCh := make(chan personDone, len(people))

	// Launch all people in parallel.
	var wg sync.WaitGroup
	for _, p := range people {
		wg.Add(1)
		go func(person memory.Person) {
			defer wg.Done()
			onEvent := func(ev agent.AgentEvent) {
				select {
				case eventCh <- personEvent{person: person, event: ev}:
				case <-ctx.Done():
				}
			}
			result, err := s.runAgent(ctx, person.Prompt, "", sessionID, messages, onEvent)
			doneCh <- personDone{person: person, result: result, err: err}
		}(p)
	}

	// Close eventCh once all agents finish.
	go func() {
		wg.Wait()
		close(eventCh)
	}()

	// Drain events in the main goroutine (single writer to w — no race).
	for ev := range eventCh {
		displayName := ev.person.Username
		if displayName == "" {
			displayName = ev.person.Name
		}
		sendSSE(w, flusher, map[string]any{
			"event":      string(ev.event.Kind),
			"tool_name":  ev.event.ToolName,
			"tool_id":    ev.event.ToolID,
			"tool_input": ev.event.ToolInput,
			"content":    ev.event.Content,
			"thinking":   ev.event.Thinking,
			"is_error":   ev.event.IsError,
			"iteration":  ev.event.Iteration,
			"persona":    displayName,
		})
	}

	// All agents finished and all events drained. Send errors and final done.
	close(doneCh)
	for d := range doneCh {
		displayName := d.person.Username
		if displayName == "" {
			displayName = d.person.Name
		}
		if d.err != nil {
			sendSSE(w, flusher, map[string]any{"persona": displayName, "error": d.err.Error()})
		}
	}
	sendSSE(w, flusher, map[string]any{"done": true})
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

	// Normalize: if user pasted a full GitHub URL, extract owner/repo
	target := strings.TrimSpace(req.Name)
	if shorthand, ok := skill.ParseGitHubURL(target); ok {
		target = shorthand
	}

	var skillPath string
	var cleanup func()

	if skill.IsGitHubShorthand(target) {
		// GitHub install: download tarball, extract, import
		srcDir, err := skill.DownloadGitHub(r.Context(), target)
		if err != nil {
			writeError(w, fmt.Sprintf("GitHub download failed: %v", err), http.StatusBadGateway)
			return
		}
		// srcDir may be inside a parent temp dir — clean the parent
		cleanup = func() { os.RemoveAll(filepath.Dir(srcDir)) }
		skillPath = srcDir
	} else {
		// Bare name: try ClawHub first, fall back to npm
		client := skill.NewClawHubClient(skill.ClawHubConfig{})
		tmpDir, err := os.MkdirTemp("", "gostaff-install-*")
		if err != nil {
			writeError(w, fmt.Sprintf("temp dir: %v", err), http.StatusInternalServerError)
			return
		}
		cleanup = func() { os.RemoveAll(tmpDir) }

		dlPath, dlErr := client.DownloadSkill(r.Context(), target, tmpDir)
		if dlErr == nil {
			skillPath = dlPath
		} else {
			// ClawHub miss — try npm
			npmPath, npmErr := skill.DownloadNPM(r.Context(), target)
			if npmErr != nil {
				cleanup()
				writeError(w, fmt.Sprintf("not found on ClawHub or npm: %v", npmErr), http.StatusBadGateway)
				return
			}
			// npmPath is inside its own temp dir
			oldCleanup := cleanup
			cleanup = func() { oldCleanup(); os.RemoveAll(filepath.Dir(npmPath)) }
			skillPath = npmPath
		}
	}
	defer cleanup()

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
// combinePrompts prepends global to persona, separated by a blank line.
func combinePrompts(global, persona string) string {
	global = strings.TrimSpace(global)
	persona = strings.TrimSpace(persona)
	if global == "" {
		return persona
	}
	if persona == "" {
		return global
	}
	return global + "\n\n" + persona
}

// extractModelTag scans the text for @model-id where model-id matches a known model.
// Returns the model ID if found, or empty string.
func (s *Server) extractModelTag(text string) string {
	if s.router == nil {
		return ""
	}
	models := s.router.Models()
	for _, m := range models {
		tag := "@" + m.ID
		if strings.Contains(text, tag) {
			return m.ID
		}
	}
	return ""
}

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

// sessionTitle returns a short title derived from the first user message.
func sessionTitle(text string) string {
	t := strings.TrimSpace(text)
	// Strip leading markdown/punctuation noise
	t = strings.TrimLeft(t, "#>*`-_")
	t = strings.TrimSpace(t)
	// Take first line only
	if i := strings.IndexByte(t, '\n'); i > 0 {
		t = t[:i]
	}
	const max = 60
	if len(t) > max {
		t = t[:max]
		// Trim to last word boundary
		if i := strings.LastIndexByte(t, ' '); i > 20 {
			t = t[:i]
		}
		t += "…"
	}
	return t
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
			Title:    sessionTitle(text),
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
