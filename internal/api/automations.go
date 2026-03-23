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
	Name      string  `json:"name"`
	RRule     string  `json:"rrule"`
	StartAt   *string `json:"start_at"`
	EndAt     *string `json:"end_at"`
	Prompt    string  `json:"prompt"`
	SkillName string  `json:"skill_name"`
	Enabled   *bool   `json:"enabled"`
}

func parseOptionalTime(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	return &t
}

func computeNextRun(rrule string, from time.Time) *time.Time {
	if rrule == "" {
		return nil
	}
	sched, err := cron.Parse(rrule)
	if err != nil {
		return nil
	}
	next := sched.Next(from)
	return &next
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
	if inp.Prompt == "" && inp.SkillName == "" {
		writeError(w, "prompt or skill_name is required", http.StatusBadRequest)
		return
	}
	if inp.RRule != "" {
		if _, err := cron.Parse(inp.RRule); err != nil {
			writeError(w, fmt.Sprintf("invalid rrule: %v", err), http.StatusBadRequest)
			return
		}
	}
	startAt := parseOptionalTime(inp.StartAt)
	endAt := parseOptionalTime(inp.EndAt)

	from := time.Now()
	if startAt != nil && startAt.After(from) {
		from = *startAt
	}
	nextRunAt := computeNextRun(inp.RRule, from)

	enabled := true
	if inp.Enabled != nil {
		enabled = *inp.Enabled
	}
	id, err := s.store.CreateAutomation(r.Context(), memory.Automation{
		Name:      inp.Name,
		RRule:     inp.RRule,
		StartAt:   startAt,
		EndAt:     endAt,
		Prompt:    inp.Prompt,
		SkillName: inp.SkillName,
		Enabled:   enabled,
		NextRunAt: nextRunAt,
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
	// Allow clearing skill_name by sending empty string explicitly
	existing.SkillName = inp.SkillName
	if inp.Enabled != nil {
		existing.Enabled = *inp.Enabled
	}
	// Update start/end times if provided.
	if inp.StartAt != nil {
		existing.StartAt = parseOptionalTime(inp.StartAt)
	}
	if inp.EndAt != nil {
		existing.EndAt = parseOptionalTime(inp.EndAt)
	}
	// If rrule changed, recompute next_run_at.
	if inp.RRule != existing.RRule {
		if inp.RRule != "" {
			if _, err := cron.Parse(inp.RRule); err != nil {
				writeError(w, fmt.Sprintf("invalid rrule: %v", err), http.StatusBadRequest)
				return
			}
		}
		existing.RRule = inp.RRule
		from := time.Now()
		if existing.StartAt != nil && existing.StartAt.After(from) {
			from = *existing.StartAt
		}
		existing.NextRunAt = computeNextRun(inp.RRule, from)
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
