package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

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
			return usageMetadata{Configured: true, Available: s.accounts.MiniMaxCode != nil && s.accounts.MiniMaxCode.HasSession()}
		}
	}
	return usageMetadata{}
}

func (s *Server) requireMiniMaxAccount(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !s.minimaxUsageMetadata().Available {
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
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		account, err := s.accounts.MiniMaxCode.Account(ctx)
		if err != nil {
			view.AccountError = "MiniMax credits and Token Plan information are temporarily unavailable."
			return
		}
		view.Account = account
	}()
	go func() {
		defer wg.Done()
		panel, err := s.accounts.MiniMaxCode.CheckinStatus(ctx)
		if err != nil {
			view.CheckinError = "MiniMax check-in status is temporarily unavailable."
			return
		}
		view.Checkin = panel
	}()
	wg.Wait()
	view.FetchedAt = time.Now().UTC()
	// Independent account services can fail separately; keep the usable portion.
	status := http.StatusOK
	if view.Account == nil && view.Checkin == nil {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, view)
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
