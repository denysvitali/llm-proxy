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
	plans, err := s.zcodeUsage(r.Context())
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
