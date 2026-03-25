package api

import (
	"encoding/json"
	"net/http"

	"github.com/polymath/capabot/internal/memory"
)

func (s *Server) handleModesGet(w http.ResponseWriter, r *http.Request) {
	modes, err := s.store.ListModes(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	active := s.store.GetActiveMode(r.Context())
	writeJSON(w, map[string]any{"modes": modes, "active": active})
}

func (s *Server) handleModesPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, "mode name required", http.StatusBadRequest)
		return
	}
	var keys memory.ModeKeys
	if err := json.NewDecoder(r.Body).Decode(&keys); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.store.SetMode(r.Context(), name, keys); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModesDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, "mode name required", http.StatusBadRequest)
		return
	}
	if name == "default" || name == "chat" || name == "execute" {
		writeError(w, "cannot delete built-in mode", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteMode(r.Context(), name); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleActiveModePut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Mode == "" {
		writeError(w, "mode is required", http.StatusBadRequest)
		return
	}
	if err := s.store.SetActiveMode(r.Context(), body.Mode); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
