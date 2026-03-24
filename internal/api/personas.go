package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/polymath/capabot/internal/memory"
)

func (s *Server) handlePersonasList(w http.ResponseWriter, r *http.Request) {
	personas, err := s.store.ListPersonas(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if personas == nil {
		personas = []memory.Persona{} //nolint:staticcheck
	}
	writeJSON(w, personas)
}

func (s *Server) handlePersonasCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	id, err := s.store.CreatePersona(r.Context(), body.Name, body.Prompt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeError(w, "a persona named \""+body.Name+"\" already exists", http.StatusConflict)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) handlePersonasUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdatePersona(r.Context(), id, body.Name, body.Prompt); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePersonasDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeletePersona(r.Context(), id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
