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
			writeJSON(w, zcodePreflightResult(kind).status(), zcodebackend.ClaimOutcome{PlanID: request.PlanID, FailureKind: kind})
			return
		}
		// A transport or response error may follow a successful upstream claim.
		// Force the next read to refresh rather than presenting stale balances.
		s.zcodeUsageCache.invalidate()
		writeJSON(w, zcodeClaimUpstreamError.status(), map[string]string{"error": err.Error()})
		return
	}
	// Even an unrecognized or failed response may follow a submitted claim;
	// subsequent account reads must not reuse a pre-attempt snapshot.
	s.zcodeUsageCache.invalidate()
	writeJSON(w, zcodeOutcomeResult(outcome).status(), outcome)
}

// zcodeClaimResult is the terminal result of one plan-claim request:
//
//	preflight rejected, kind login_required  → login-required (401)
//	preflight rejected, any other kind       → rejected (400)
//	claim call errored after preflight       → upstream-error (502)
//	outcome OK or already_claimed            → claimed (200)
//	outcome captcha or invalid_request       → rejected (400)
//	outcome login_required                   → login-required (401)
//	any other outcome                        → conflict (409)
type zcodeClaimResult int

const (
	zcodeClaimClaimed zcodeClaimResult = iota
	zcodeClaimRejected
	zcodeClaimLoginRequired
	zcodeClaimUpstreamError
	zcodeClaimConflict
)

func (r zcodeClaimResult) String() string {
	switch r {
	case zcodeClaimClaimed:
		return "claimed"
	case zcodeClaimRejected:
		return "rejected"
	case zcodeClaimLoginRequired:
		return "login-required"
	case zcodeClaimUpstreamError:
		return "upstream-error"
	case zcodeClaimConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

func (r zcodeClaimResult) status() int {
	switch r {
	case zcodeClaimClaimed:
		return http.StatusOK
	case zcodeClaimRejected:
		return http.StatusBadRequest
	case zcodeClaimLoginRequired:
		return http.StatusUnauthorized
	case zcodeClaimUpstreamError:
		return http.StatusBadGateway
	default:
		return http.StatusConflict
	}
}

func zcodePreflightResult(kind string) zcodeClaimResult {
	if kind == "login_required" {
		return zcodeClaimLoginRequired
	}
	return zcodeClaimRejected
}

func zcodeOutcomeResult(outcome zcodebackend.ClaimOutcome) zcodeClaimResult {
	switch {
	case outcome.OK || outcome.FailureKind == "already_claimed":
		return zcodeClaimClaimed
	case outcome.FailureKind == "captcha" || outcome.FailureKind == "invalid_request":
		return zcodeClaimRejected
	case outcome.FailureKind == "login_required":
		return zcodeClaimLoginRequired
	default:
		return zcodeClaimConflict
	}
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
