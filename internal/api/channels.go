package api

import (
	"encoding/json"
	"net/http"

	"github.com/polymath/gostaff/internal/memory"
)

func (s *Server) handleChannelsList(w http.ResponseWriter, r *http.Request) {
	configs, err := s.store.ListChannelConfigs(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if configs == nil {
		configs = []memory.ChannelConfig{}
	}
	writeJSON(w, configs)
}

func (s *Server) handleChannelGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, "channel id is required", http.StatusBadRequest)
		return
	}
	cfg, err := s.store.GetChannelConfig(r.Context(), id)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cfg == nil {
		writeError(w, "channel not found", http.StatusNotFound)
		return
	}
	writeJSON(w, cfg)
}

func (s *Server) handleChannelSet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, "channel id is required", http.StatusBadRequest)
		return
	}
	var cfg memory.ChannelConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	cfg.ChannelID = id
	if err := s.store.SetChannelConfig(r.Context(), cfg); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

func (s *Server) handleChannelDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, "channel id is required", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteChannelBinding(r.Context(), id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"success": true})
}
