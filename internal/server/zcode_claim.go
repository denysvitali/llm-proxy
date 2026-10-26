package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

func (s *Server) zcodeClaim(w http.ResponseWriter, r *http.Request) {
	if s.accounts.ZCode == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ZCode account management is unavailable"})
		return
	}
	var request struct {
		PlanID string `json:"plan_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ZCode plan claim request"})
		return
	}
	request.PlanID = strings.TrimSpace(request.PlanID)
	if request.PlanID == "" || len(request.PlanID) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid ZCode plan_id is required"})
		return
	}
	outcome, err := s.accounts.ZCode.ClaimPlan(r.Context(), request.PlanID)
	if err != nil {
		if kind := zcodebackend.ClaimPreflightFailure(err); kind != "" {
			status := http.StatusBadRequest
			if kind == "login_required" {
				status = http.StatusUnauthorized
			}
			writeJSON(w, status, zcodebackend.ClaimOutcome{PlanID: request.PlanID, FailureKind: kind})
			return
		}
		// A transport or response error may follow a successful upstream claim.
		// Force the next read to refresh rather than presenting stale balances.
		s.zcodeUsageCache.invalidate()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusConflict
	// Even an unrecognized or failed response may follow a submitted claim;
	// subsequent account reads must not reuse a pre-attempt snapshot.
	s.zcodeUsageCache.invalidate()
	if outcome.OK || outcome.FailureKind == "already_claimed" {
		status = http.StatusOK
	} else if outcome.FailureKind == "captcha" || outcome.FailureKind == "invalid_request" {
		status = http.StatusBadRequest
	} else if outcome.FailureKind == "login_required" {
		status = http.StatusUnauthorized
	}
	writeJSON(w, status, outcome)
}

// zcodeOffers lists the claimable offers ZCode advertises for the signed-in
// account, so a plan_id can be picked before POSTing /api/zcode/claim.
func (s *Server) zcodeOffers(w http.ResponseWriter, r *http.Request) {
	if s.accounts.ZCode == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ZCode account management is unavailable"})
		return
	}
	plans, err := s.accounts.ZCode.PreviewPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}
