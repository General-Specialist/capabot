package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/memory"
)

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
		if err := s.store.UpsertSession(ctx, memory.Session{
			ID:       sessionID,
			TenantID: tenantID,
			Channel:  "web",
			Title:    sessionTitle(text),
		}); err != nil {
			s.logger.Warn().Err(err).Str("session", sessionID).Msg("failed to upsert session")
		}
		if _, err := s.store.SaveMessage(ctx, memory.Message{
			SessionID: sessionID,
			Role:      "user",
			Content:   text,
		}); err != nil {
			s.logger.Warn().Err(err).Str("session", sessionID).Msg("failed to save user message")
		}
	}
	return sessionID
}
