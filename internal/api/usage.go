package api

import (
	"net/http"
	"time"

	"github.com/polymath/capabot/internal/llm"
	"github.com/polymath/capabot/internal/memory"
)

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, "invalid since parameter", http.StatusBadRequest)
			return
		}
		since = t
	}
	rows, err := s.store.GetUsageSummary(r.Context(), since)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []memory.UsageSummary{}
	}
	writeJSON(w, rows)
}

// handleCredits fetches live account spend from providers that support it.
func (s *Server) handleCredits(w http.ResponseWriter, r *http.Request) {
	type creditEntry struct {
		Provider     string  `json:"provider"`
		TotalUsedUSD float64 `json:"total_used_usd"`
		LimitUSD     float64 `json:"limit_usd"`
	}
	var results []creditEntry
	if s.router != nil {
		for name, p := range s.router.ProviderMap() {
			if cf, ok := p.(llm.CreditFetcher); ok {
				info, err := cf.FetchCredits(r.Context())
				if err == nil {
					results = append(results, creditEntry{
						Provider:     name,
						TotalUsedUSD: info.TotalUsedUSD,
						LimitUSD:     info.LimitUSD,
					})
				}
			}
		}
	}
	if results == nil {
		results = []creditEntry{}
	}
	writeJSON(w, results)
}
