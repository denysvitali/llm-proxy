package zcode

import (
	"context"
	"errors"
	"time"
)

// AutoClaimStatus contains process-local scheduler observations. It resets on restart;
// durable markers, rather than these counters, control distributed deduplication.
// LastAttemptUnix records a durable reservation immediately before mutation,
// not proof that a request was sent or accepted. Zero timestamps mean unobserved.
type AutoClaimStatus struct {
	LastCheckUnix   int64             `json:"last_check_unix"`
	LastAttemptUnix int64             `json:"last_attempt_unix"`
	LastSuccessUnix int64             `json:"last_success_unix"`
	LastOutcome     string            `json:"last_outcome"`
	LastReason      string            `json:"last_reason"`
	ReasonCounts    map[string]uint64 `json:"reason_counts"`
}

// AutoClaimStatus returns an independent, concurrency-safe diagnostic snapshot.
func (m *Manager) AutoClaimStatus() AutoClaimStatus {
	m.schedulerMu.Lock()
	defer m.schedulerMu.Unlock()
	result := m.schedulerStatus
	result.ReasonCounts = make(map[string]uint64, len(m.schedulerStatus.ReasonCounts))
	for reason, count := range m.schedulerStatus.ReasonCounts {
		result.ReasonCounts[reason] = count
	}
	return result
}

func (m *Manager) noteSchedulerAttempt() {
	m.schedulerMu.Lock()
	defer m.schedulerMu.Unlock()
	m.schedulerStatus.LastAttemptUnix = time.Now().Unix()
}

func (m *Manager) noteSchedulerResult(outcome, reason string) {
	m.schedulerMu.Lock()
	defer m.schedulerMu.Unlock()
	status := &m.schedulerStatus
	status.LastCheckUnix = time.Now().Unix()
	status.LastOutcome, status.LastReason = outcome, reason
	if outcome == "success" {
		status.LastSuccessUnix = status.LastCheckUnix
	}
	if status.ReasonCounts == nil {
		status.ReasonCounts = make(map[string]uint64)
	}
	status.ReasonCounts[reason]++
}

// schedulerFailure preserves errors.Is and existing user-facing error text.
// Reasons are supplied only by fixed local literals, never upstream payloads.
type schedulerFailure struct {
	reason string
	err    error
}

func (e *schedulerFailure) Error() string           { return e.err.Error() }
func (e *schedulerFailure) Unwrap() error           { return e.err }
func schedulerError(reason string, err error) error { return &schedulerFailure{reason, err} }
func schedulerReason(err error) string {
	if err == nil {
		return "claimed"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var failure *schedulerFailure
	if errors.As(err, &failure) {
		return failure.reason
	}
	return "unexpected_error"
}
