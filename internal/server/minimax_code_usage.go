package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	minimaxcodebackend "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
)

type minimaxUsageView struct {
	Account      *minimaxcodebackend.AccountUsage `json:"account,omitempty"`
	Checkin      *minimaxcodebackend.CheckinPanel `json:"checkin,omitempty"`
	AccountError string                           `json:"accountError,omitempty"`
	CheckinError string                           `json:"checkinError,omitempty"`
	FetchedAt    time.Time                        `json:"fetchedAt"`
}

// Metadata is local so an account-service outage cannot delay the dashboard shell.
func (s *Server) minimaxUsageMetadata() usageMetadata {
	for _, bc := range s.cfg.Backends {
		if bc.Type == "minimax-code" && bc.IsEnabled() {
			if s.accounts.MiniMaxCode == nil || !s.accounts.MiniMaxCode.HasSession() {
				return usageUnavailable.metadata("")
			}
			return usageAvailable.metadata("")
		}
	}
	return usageUnconfigured.metadata("")
}

func (s *Server) requireMiniMaxAccount(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.minimaxUsageMetadata().availability() != usageAvailable {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "MiniMax Code account is unavailable; enable the backend and sign in."})
		return false
	}
	return true
}

func (s *Server) handleMiniMaxCodeUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireMiniMaxAccount(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var view minimaxUsageView
	var accountErr, checkinErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		account, err := s.accounts.MiniMaxCode.Account(ctx)
		if err != nil {
			accountErr = err
			view.AccountError = "MiniMax credits and Token Plan information are temporarily unavailable."
			return
		}
		view.Account = account
	}()
	go func() {
		defer wg.Done()
		panel, err := s.accounts.MiniMaxCode.CheckinStatus(ctx)
		if err != nil {
			checkinErr = err
			view.CheckinError = "MiniMax check-in status is temporarily unavailable."
			return
		}
		view.Checkin = panel
	}()
	wg.Wait()
	view.FetchedAt = time.Now().UTC()
	// Independent account services can fail separately; keep the usable portion.
	result := classifyMiniMaxUsage(view.Account != nil, view.Checkin != nil, accountErr, checkinErr)
	if result == minimaxUsageSessionExpired {
		// The session itself is dead (refresh rejected or signed out): the
		// operator must sign in again, retrying the gateway will not help.
		view.AccountError = "MiniMax Code session expired; sign in again from the dashboard."
		view.CheckinError = ""
	}
	writeJSON(w, result.status(), view)
}

// minimaxUsageResult combines the two independent account reads:
//
//	both succeeded                         → complete (200)
//	exactly one succeeded                  → partial (200, failed half carries its error)
//	both failed, not both terminal         → failed (502)
//	both failed with terminal session errs → session-expired (409)
type minimaxUsageResult int

const (
	minimaxUsageComplete minimaxUsageResult = iota
	minimaxUsagePartial
	minimaxUsageFailed
	minimaxUsageSessionExpired
)

func (r minimaxUsageResult) String() string {
	switch r {
	case minimaxUsageComplete:
		return "complete"
	case minimaxUsagePartial:
		return "partial"
	case minimaxUsageFailed:
		return "failed"
	case minimaxUsageSessionExpired:
		return "session-expired"
	default:
		return "unknown"
	}
}

func (r minimaxUsageResult) status() int {
	switch r {
	case minimaxUsageFailed:
		return http.StatusBadGateway
	case minimaxUsageSessionExpired:
		return http.StatusConflict
	default:
		return http.StatusOK
	}
}

// classifyMiniMaxUsage decides from which halves produced a value; errors
// only distinguish a dead session when neither half is usable.
func classifyMiniMaxUsage(hasAccount, hasCheckin bool, accountErr, checkinErr error) minimaxUsageResult {
	switch {
	case hasAccount && hasCheckin:
		return minimaxUsageComplete
	case hasAccount || hasCheckin:
		return minimaxUsagePartial
	case backend.IsTerminal(accountErr) && backend.IsTerminal(checkinErr):
		return minimaxUsageSessionExpired
	default:
		return minimaxUsageFailed
	}
}

func (s *Server) handleMiniMaxCodeCheckin(w http.ResponseWriter, r *http.Request) {
	if !s.requireMiniMaxAccount(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	claim, err := s.accounts.MiniMaxCode.ClaimCheckin(ctx)
	if err != nil {
		status := http.StatusBadGateway
		message := "MiniMax check-in could not be confirmed. Refresh the status before trying again."
		if errors.Is(err, minimaxcodebackend.ErrCheckinInProgress) {
			status, message = http.StatusConflict, "A MiniMax check-in is already in progress."
		} else if errors.Is(err, minimaxcodebackend.ErrCheckinAlreadyClaimed) {
			status, message = http.StatusConflict, "Already checked in to MiniMax today."
		} else if errors.Is(err, minimaxcodebackend.ErrCheckinUnavailable) {
			status, message = http.StatusConflict, "MiniMax check-in is not available right now."
		}
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	writeJSON(w, http.StatusOK, claim)
}
