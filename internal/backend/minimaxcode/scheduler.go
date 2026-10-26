package minimaxcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

var ErrAutoCheckinAlreadyAttempted = errors.New("MiniMax Code automatic check-in was already attempted today")

const autoCheckinInterval = time.Hour

// RunAutoCheckin checks once at startup and then once per hour. A durable
// per-account, per-UTC-day marker prevents automatic resubmission after an
// attempt whose upstream result is unknown.
func (m *Manager) RunAutoCheckin(ctx context.Context, log *logrus.Logger) {
	m.runAutoCheckin(ctx, log, autoCheckinInterval)
}

func (m *Manager) runAutoCheckin(ctx context.Context, log *logrus.Logger, interval time.Duration) {
	if log == nil {
		log = logrus.StandardLogger()
	}
	if interval <= 0 {
		interval = autoCheckinInterval
	}
	log.WithFields(logrus.Fields{"interval": interval.String(), "day_basis": "UTC"}).Info("MiniMax Code automatic check-in scheduler started")
	check := func() {
		if ctx.Err() != nil {
			return
		}
		if !m.HasSession() {
			m.noteSchedulerResult("skipped", "signed_out")
			log.WithFields(logrus.Fields{"outcome": "skipped", "reason": "signed_out"}).Info("MiniMax Code automatic check-in skipped")
			return
		}
		_, err := m.claimCheckin(ctx, func(userID string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := createCheckinMarker(m.Store.Path, userID, time.Now().UTC()); err != nil {
				if errors.Is(err, ErrAutoCheckinAlreadyAttempted) {
					return schedulerError("already_attempted", err)
				}
				return schedulerError("marker_failed", err)
			}
			m.noteSchedulerAttempt()
			return nil
		})
		outcome, reason := "failed", schedulerReason(err)
		switch {
		case err == nil:
			outcome = "success"
		case errors.Is(err, ErrAutoCheckinAlreadyAttempted):
			outcome, reason = "already_claimed", "already_attempted"
		case errors.Is(err, ErrCheckinAlreadyClaimed):
			outcome, reason = "already_claimed", "already_claimed"
		case errors.Is(err, ErrCheckinUnavailable):
			outcome, reason = "unavailable", "no_reward_available"
		case errors.Is(err, ErrCheckinInProgress):
			outcome, reason = "skipped", "claim_in_progress"
		case ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)):
			// Parent cancellation or deadline is expected during shutdown.
			return
		}
		m.noteSchedulerResult(outcome, reason)
		fields := logrus.Fields{"outcome": outcome, "reason": reason}
		if outcome == "failed" {
			// Error strings may contain account or upstream details; use fixed categories.
			log.WithFields(fields).Warn("MiniMax Code automatic check-in failed")
		} else if outcome == "success" {
			log.WithFields(fields).Info("MiniMax Code automatic check-in completed")
		} else {
			log.WithFields(fields).Info("MiniMax Code automatic check-in skipped")
		}
	}
	check()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func createCheckinMarker(storePath, userID string, now time.Time) error {
	if userID == "" {
		return errors.New("missing account identity")
	}
	day := now.UTC().Format("2006-01-02")
	userHash := sha256.Sum256([]byte(userID))
	dir := storePath + ".checkin"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	// Persist the marker directory entry before a reward mutation can begin.
	if err := syncCheckinDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	path := filepath.Join(dir, day+"-"+hex.EncodeToString(userHash[:]))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return ErrAutoCheckinAlreadyAttempted
	}
	if err != nil {
		return err
	}
	// Keep the file once created, including when its write or later POST fails.
	_, writeErr := file.WriteString(day + "\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncCheckinDirectory(dir)
}

func syncCheckinDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
