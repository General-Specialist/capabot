package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/polymath/capabot/internal/cron"
	"github.com/polymath/capabot/internal/memory"
)

type automationInput struct {
	Name    string `json:"name"`
	Cron    string `json:"cron"`
	Prompt  string `json:"prompt"`
	Enabled *bool  `json:"enabled"`
}

func (s *Server) handleAutomationsList(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, []any{})
		return
	}
	list, err := s.store.ListAutomations(r.Context())
	if err != nil {
		writeError(w, fmt.Sprintf("listing automations: %v", err), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []memory.Automation{}
	}
	writeJSON(w, list)
}

func (s *Server) handleAutomationsCreate(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, "store not available", http.StatusServiceUnavailable)
		return
	}
	var inp automationInput
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if inp.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if inp.Prompt == "" {
		writeError(w, "prompt is required", http.StatusBadRequest)
		return
	}
	sched, err := cron.Parse(inp.Cron)
	if err != nil {
		writeError(w, fmt.Sprintf("invalid cron expression: %v", err), http.StatusBadRequest)
		return
	}
	next := sched.Next(time.Now())
	enabled := true
	if inp.Enabled != nil {
		enabled = *inp.Enabled
	}
	id, err := s.store.CreateAutomation(r.Context(), memory.Automation{
		Name:      inp.Name,
		Cron:      inp.Cron,
		Prompt:    inp.Prompt,
		Enabled:   enabled,
		NextRunAt: &next,
	})
	if err != nil {
		writeError(w, fmt.Sprintf("creating automation: %v", err), http.StatusInternalServerError)
		return
	}
	auto, err := s.store.GetAutomation(r.Context(), id)
	if err != nil {
		writeError(w, fmt.Sprintf("fetching automation: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, auto)
}

func (s *Server) handleAutomationsGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	auto, err := s.store.GetAutomation(r.Context(), id)
	if err != nil {
		writeError(w, fmt.Sprintf("automation not found: %v", err), http.StatusNotFound)
		return
	}
	writeJSON(w, auto)
}

func (s *Server) handleAutomationsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	existing, err := s.store.GetAutomation(r.Context(), id)
	if err != nil {
		writeError(w, "automation not found", http.StatusNotFound)
		return
	}
	var inp automationInput
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if inp.Name != "" {
		existing.Name = inp.Name
	}
	if inp.Prompt != "" {
		existing.Prompt = inp.Prompt
	}
	if inp.Enabled != nil {
		existing.Enabled = *inp.Enabled
	}
	// If cron changed, recompute next_run_at
	if inp.Cron != "" && inp.Cron != existing.Cron {
		sched, err := cron.Parse(inp.Cron)
		if err != nil {
			writeError(w, fmt.Sprintf("invalid cron expression: %v", err), http.StatusBadRequest)
			return
		}
		next := sched.Next(time.Now())
		existing.Cron = inp.Cron
		existing.NextRunAt = &next
	}
	if err := s.store.UpdateAutomation(r.Context(), existing); err != nil {
		writeError(w, fmt.Sprintf("updating automation: %v", err), http.StatusInternalServerError)
		return
	}
	updated, _ := s.store.GetAutomation(r.Context(), id)
	writeJSON(w, updated)
}

func (s *Server) handleAutomationsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteAutomation(r.Context(), id); err != nil {
		writeError(w, fmt.Sprintf("deleting automation: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

func (s *Server) handleAutomationsTrigger(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if s.scheduler == nil {
		writeError(w, "scheduler not available", http.StatusServiceUnavailable)
		return
	}
	s.scheduler.TriggerNow(id)
	writeJSON(w, map[string]any{"triggered": true})
}

func (s *Server) handleAutomationsRuns(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	runs, err := s.store.ListAutomationRuns(r.Context(), id, 20)
	if err != nil {
		writeError(w, fmt.Sprintf("listing runs: %v", err), http.StatusInternalServerError)
		return
	}
	if runs == nil {
		runs = []memory.AutomationRun{}
	}
	writeJSON(w, runs)
}
