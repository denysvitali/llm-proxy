package server

import (
	"errors"
	"net/http"
	"time"

	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

type zcodeQuotaView struct {
	Plans     []zcodebackend.PlanUsage   `json:"plans"`
	Balances  []zcodebackend.PlanBalance `json:"balances"`
	FetchedAt time.Time                  `json:"fetchedAt"`
}

// handleZcodeQuota exposes the live ZCode quota buckets. /api/zcode/balance
// is retained as a path alias because it mirrors the upstream endpoint name.
func (s *Server) handleZcodeQuota(w http.ResponseWriter, r *http.Request) {
	quota, err := s.zcodeQuota(r.Context())
	if errors.Is(err, errZcodeQuotaUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": sanitizeUsageError(err)})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, zcodeQuotaView{
		Plans:     quota.Plans,
		Balances:  quota.Balances,
		FetchedAt: time.Now().UTC(),
	})
}

var errZcodeQuotaUnavailable = errors.New("ZCode account quota is unavailable")
