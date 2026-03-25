package api

import (
	"encoding/json"
	"net/http"

	"github.com/polymath/capabot/internal/memory"
)

func (s *Server) handleMemoryList(w http.ResponseWriter, r *http.Request) {
	tenantID := TenantIDFromContext(r.Context())
	entries, err := s.store.ListMemory(r.Context(), tenantID)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []memory.MemoryEntry{}
	}
	writeJSON(w, entries)
}

func (s *Server) handleMemoryUpsert(w http.ResponseWriter, r *http.Request) {
	tenantID := TenantIDFromContext(r.Context())
	key := r.PathValue("key")
	if key == "" {
		writeError(w, "key is required", http.StatusBadRequest)
		return
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	entry := memory.MemoryEntry{TenantID: tenantID, Key: key, Value: body.Value}
	if err := s.store.StoreMemory(r.Context(), entry); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"key": key})
}

func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	tenantID := TenantIDFromContext(r.Context())
	key := r.PathValue("key")
	if key == "" {
		writeError(w, "key is required", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteMemory(r.Context(), tenantID, key); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"deleted": true})
}
