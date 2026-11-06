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
	if outcome == autoClaimSuccess.String() {
		status.LastSuccessUnix = status.LastCheckUnix
	}
	if status.ReasonCounts == nil {
		status.ReasonCounts = make(map[string]uint64)
	}
	status.ReasonCounts[reason]++
}

// autoClaimReason is the fixed, metric-facing reason recorded for one
// scheduler check (AutoClaimStatus.LastReason and ReasonCounts keys). Its
// String values are part of the metrics contract and must stay byte-identical.
//
// One autoClaim check passes these gates in order, stopping at the first
// failing one (see Manager.autoClaim):
//
//	before_claim_window   UTC hour < 11                       -> unavailable
//	signed_out            no stored session                   -> unavailable
//	authentication_failed session expired or unreadable       -> failed
//	identity_unavailable  JWT has no subject                  -> failed
//	already_attempted     today's durable marker exists       -> already_attempted
//	marker_failed         marker cannot be checked or created -> failed
//	preview_failed        billing preview request failed      -> failed
//	ambiguous_offer       more than one daily free offer      -> unavailable
//	no_eligible_offer     no daily free offer                 -> unavailable
//	claim preparation (captcha_preparation_failed, claim_preparation_failed,
//	  account_changed), then the marker is reserved and the claim POSTed
//	  (claim_request_failed, claim_response_failed, claim_response_invalid)
//	claim_<kind>          upstream rejected the claim         -> failed
//	claimed               upstream accepted the claim         -> success
//
// deadline_exceeded, canceled and unexpected_error classify errors that
// carry no gate.
type autoClaimReason int

const (
	reasonUnexpectedError autoClaimReason = iota
	reasonClaimed
	reasonDeadlineExceeded
	reasonCanceled
	reasonBeforeClaimWindow
	reasonSignedOut
	reasonAuthenticationFailed
	reasonIdentityUnavailable
	reasonAlreadyAttempted
	reasonMarkerFailed
	reasonPreviewFailed
	reasonAmbiguousOffer
	reasonNoEligibleOffer
	reasonCaptchaPreparationFailed
	reasonClaimPreparationFailed
	reasonAccountChanged
	reasonClaimRequestFailed
	reasonClaimResponseFailed
	reasonClaimResponseInvalid
	// reasonClaimRejected renders as "claim_" + the ClaimOutcome.FailureKind.
	reasonClaimRejected
)

func (r autoClaimReason) String() string {
	switch r {
	case reasonClaimed:
		return "claimed"
	case reasonDeadlineExceeded:
		return "deadline_exceeded"
	case reasonCanceled:
		return "canceled"
	case reasonBeforeClaimWindow:
		return "before_claim_window"
	case reasonSignedOut:
		return "signed_out"
	case reasonAuthenticationFailed:
		return "authentication_failed"
	case reasonIdentityUnavailable:
		return "identity_unavailable"
	case reasonAlreadyAttempted:
		return "already_attempted"
	case reasonMarkerFailed:
		return "marker_failed"
	case reasonPreviewFailed:
		return "preview_failed"
	case reasonAmbiguousOffer:
		return "ambiguous_offer"
	case reasonNoEligibleOffer:
		return "no_eligible_offer"
	case reasonCaptchaPreparationFailed:
		return "captcha_preparation_failed"
	case reasonClaimPreparationFailed:
		return "claim_preparation_failed"
	case reasonAccountChanged:
		return "account_changed"
	case reasonClaimRequestFailed:
		return "claim_request_failed"
	case reasonClaimResponseFailed:
		return "claim_response_failed"
	case reasonClaimResponseInvalid:
		return "claim_response_invalid"
	case reasonClaimRejected:
		return "claim_rejected"
	default:
		return "unexpected_error"
	}
}

// autoClaimOutcome is the metric-facing outcome of one scheduler check
// (AutoClaimStatus.LastOutcome). String values must stay byte-identical.
//
//	nil error                       -> success
//	errAutoClaimAttempted in chain  -> already_attempted
//	errAutoClaimUnavailable         -> unavailable
//	anything else                   -> failed (not recorded when the
//	                                   scheduler itself is shutting down)
type autoClaimOutcome int

const (
	autoClaimFailed autoClaimOutcome = iota
	autoClaimSuccess
	autoClaimAlreadyAttempted
	autoClaimUnavailable
)

func (o autoClaimOutcome) String() string {
	switch o {
	case autoClaimSuccess:
		return "success"
	case autoClaimAlreadyAttempted:
		return "already_attempted"
	case autoClaimUnavailable:
		return "unavailable"
	default:
		return "failed"
	}
}

func classifyAutoClaimOutcome(err error) autoClaimOutcome {
	switch {
	case err == nil:
		return autoClaimSuccess
	case errors.Is(err, errAutoClaimAttempted):
		return autoClaimAlreadyAttempted
	case errors.Is(err, errAutoClaimUnavailable):
		return autoClaimUnavailable
	default:
		return autoClaimFailed
	}
}

// schedulerFailure preserves errors.Is and existing user-facing error text.
// Reasons are fixed local constants, never upstream payloads; kind is only
// set for reasonClaimRejected and comes from claimFailure.
type schedulerFailure struct {
	reason autoClaimReason
	kind   string
	err    error
}

func (e *schedulerFailure) Error() string { return e.err.Error() }
func (e *schedulerFailure) Unwrap() error { return e.err }

func (e *schedulerFailure) reasonString() string {
	if e.reason == reasonClaimRejected {
		return "claim_" + e.kind
	}
	return e.reason.String()
}

func schedulerError(reason autoClaimReason, err error) error {
	return &schedulerFailure{reason: reason, err: err}
}

func claimRejectedError(kind string, err error) error {
	return &schedulerFailure{reason: reasonClaimRejected, kind: kind, err: err}
}

func schedulerReason(err error) string {
	if err == nil {
		return reasonClaimed.String()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return reasonDeadlineExceeded.String()
	}
	if errors.Is(err, context.Canceled) {
		return reasonCanceled.String()
	}
	var failure *schedulerFailure
	if errors.As(err, &failure) {
		return failure.reasonString()
	}
	return reasonUnexpectedError.String()
}
