package server

import (
	"errors"
	"net/http"
	"time"

	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

type zcodeUsageView struct {
	Plans     []zcodebackend.PlanUsage `json:"plans"`
	FetchedAt time.Time                `json:"fetchedAt"`
}

func (s *Server) handleZcodeUsage(w http.ResponseWriter, r *http.Request) {
	plans, err := s.zcodeUsageCache.plans(r.Context())
	if errors.Is(err, errZcodeUsageUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": sanitizeUsageError(err)})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, zcodeUsageView{Plans: plans, FetchedAt: time.Now().UTC()})
}

var errZcodeUsageUnavailable = errors.New("ZCode account usage is unavailable")

// zcodeUsageMetadata reports whether the dashboard should render the ZCode
// plan usage card. Like the grok metadata, it carries only presence and a
// sanitized failure reason — never the upstream response.
func (s *Server) zcodeUsageMetadata(r *http.Request) usageMetadata {
	configured := false
	for _, bc := range s.cfg.Backends {
		if bc.Type != zcodeUsageBackendName || !bc.IsEnabled() {
			continue
		}
		configured = true
		break
	}
	if !configured {
		return usageMetadata{}
	}
	if s.accounts.ZCode == nil || !s.accounts.ZCode.HasSession() {
		return usageMetadata{Configured: true}
	}
	if _, err := s.zcodeUsageCache.plans(r.Context()); err != nil {
		return usageMetadata{Configured: true, Available: false, Error: err.Error()}
	}
	return usageMetadata{Configured: true, Available: true}
}
