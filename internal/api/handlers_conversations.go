package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

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
