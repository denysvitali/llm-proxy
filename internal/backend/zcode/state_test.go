package zcode

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestUnusualActivityStateTransitions(t *testing.T) {
	client := New("", "unused")
	now := time.Now()
	state := func() unusualActivityState {
		client.blockedMu.Lock()
		defer client.blockedMu.Unlock()
		return client.unusualActivityStateLocked(time.Now())
	}
	if got := state(); got != unusualActivityClear {
		t.Fatalf("initial state = %v, want clear", got)
	}
	client.markUnusualActivity()
	if got := state(); got != unusualActivityBlocked {
		t.Fatalf("after 3012 state = %v, want blocked", got)
	}
	client.blockedUntil = now.Add(-time.Second)
	if got := state(); got != unusualActivityLapsed {
		t.Fatalf("after deadline state = %v, want lapsed", got)
	}
	if _, blocked := client.unusualActivityBlock(); blocked {
		t.Fatal("lapsed cooldown still blocks requests")
	}
	if client.blockedStrikes != 1 {
		t.Fatalf("lapsed strikes = %d, want 1 kept", client.blockedStrikes)
	}
	client.clearUnusualActivity()
	if got := state(); got != unusualActivityClear || client.blockedStrikes != 0 {
		t.Fatalf("after success state = %v strikes = %d, want clear and 0", got, client.blockedStrikes)
	}
}

func TestUnusualActivityPause(t *testing.T) {
	for strikes, want := range []time.Duration{
		15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 6 * time.Hour, 6 * time.Hour,
	} {
		if got := unusualActivityPause(strikes); got != want {
			t.Errorf("unusualActivityPause(%d) = %v, want %v", strikes, got, want)
		}
	}
}

func TestCaptchaRecoveryTransitions(t *testing.T) {
	if r := newCaptchaRecovery(false); r.state != captchaRecoveryResolved {
		t.Fatalf("unchallenged start = %v, want resolved", r.state)
	}
	r := newCaptchaRecovery(true)
	r.noProof()
	if r.state != captchaRecoveryNoProof {
		t.Fatalf("no proof = %v, want no_proof", r.state)
	}

	r = newCaptchaRecovery(true)
	r.retried(false, true)
	if r.state != captchaRecoveryResolved || r.proofs != 1 {
		t.Fatalf("accepted retry = %v/%d, want resolved/1", r.state, r.proofs)
	}

	r = newCaptchaRecovery(true)
	r.retried(true, false)
	if r.state != captchaRecoveryManualRejected {
		t.Fatalf("rejected manual proof = %v, want manual_rejected", r.state)
	}

	r = newCaptchaRecovery(true)
	for i := 1; i < maxAutomaticModelCaptchaRetries; i++ {
		r.retried(true, true)
		if r.state != captchaRecoveryChallenged {
			t.Fatalf("solver retry %d = %v, want challenged", i, r.state)
		}
	}
	r.retried(true, true)
	if r.state != captchaRecoveryExhausted || r.proofs != maxAutomaticModelCaptchaRetries {
		t.Fatalf("final solver retry = %v/%d, want exhausted/%d", r.state, r.proofs, maxAutomaticModelCaptchaRetries)
	}
	// Terminal states ignore further events.
	r.retried(false, true)
	r.noProof()
	if r.state != captchaRecoveryExhausted || r.proofs != maxAutomaticModelCaptchaRetries {
		t.Fatalf("terminal state changed to %v/%d", r.state, r.proofs)
	}
}

func testJWT(t *testing.T, claims string) string {
	t.Helper()
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString([]byte(claims)) + ".sig"
}

func TestClassifySession(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour).Unix()
	soon := now.Add(time.Minute).Unix()
	for _, test := range []struct {
		name        string
		credentials *Credentials
		want        sessionState
	}{
		{"no file", nil, sessionNoFile},
		{"blank token", &Credentials{AccessToken: "  "}, sessionNoToken},
		{"explicit future expiry", &Credentials{AccessToken: "opaque", ExpiresAt: future}, sessionActive},
		{"explicit expiry inside margin", &Credentials{AccessToken: "opaque", ExpiresAt: soon}, sessionExpiring},
		{"derived from JWT", &Credentials{AccessToken: testJWT(t, fmt.Sprintf(`{"exp":%d}`, soon))}, sessionExpiring},
		{"JWT without exp", &Credentials{AccessToken: testJWT(t, `{"sub":"a"}`)}, sessionActive},
		{"opaque token without expiry", &Credentials{AccessToken: "opaque"}, sessionActive},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := classifySession(test.credentials, now)
			if got != test.want {
				t.Fatalf("classifySession = %v, want %v", got, test.want)
			}
			if got.signedIn() != (test.want == sessionActive || test.want == sessionExpiring) {
				t.Fatalf("signedIn() = %v for %v", got.signedIn(), got)
			}
		})
	}
}

func TestClassifyCaptchaProof(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name     string
		present  bool
		issuedAt time.Time
		want     captchaProofState
	}{
		{"absent", false, now, captchaProofAbsent},
		{"fresh", true, now.Add(-time.Second), captchaProofFresh},
		{"at ttl", true, now.Add(-captchaTTL), captchaProofStale},
		{"future issue time", true, now.Add(time.Minute), captchaProofStale},
	} {
		if got := classifyCaptchaProof(test.present, test.issuedAt); got != test.want {
			t.Errorf("%s: classifyCaptchaProof = %v, want %v", test.name, got, test.want)
		}
	}
	if isFreshCaptcha("   ", now) {
		t.Error("whitespace proof treated as fresh")
	}
}

func TestCaptchaProofSource(t *testing.T) {
	m := NewManager(t.TempDir() + "/auth.json")
	m.CaptchaSolverURL = " "
	if m.captchaProofSource() != captchaFromBrowserCache || m.CaptchaSolverConfigured() {
		t.Fatal("blank solver URL selected the solver")
	}
	m.CaptchaSolverURL = "http://127.0.0.1:1/solve"
	if m.captchaProofSource() != captchaFromSolver || !m.CaptchaSolverConfigured() {
		t.Fatal("configured solver URL did not select the solver")
	}
}

func TestParseOAuthPollStatus(t *testing.T) {
	for status, want := range map[string]oauthPollState{
		"": oauthPollPending, " Pending ": oauthPollPending, "READY": oauthPollReady,
		"failed": oauthPollRejected, "expired": oauthPollRejected, "denied": oauthPollRejected,
		"approved": oauthPollUnknown,
	} {
		if got := parseOAuthPollStatus(status); got != want {
			t.Errorf("parseOAuthPollStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestAutoClaimOutcomeAndReasonStrings(t *testing.T) {
	for err, want := range map[error]string{
		nil:                     "success",
		errAutoClaimAttempted:   "already_attempted",
		errAutoClaimUnavailable: "unavailable",
		errors.New("boom"):      "failed",
		schedulerError(reasonSignedOut, errAutoClaimUnavailable): "unavailable",
	} {
		if got := classifyAutoClaimOutcome(err).String(); got != want {
			t.Errorf("classifyAutoClaimOutcome(%v) = %q, want %q", err, got, want)
		}
	}
	wantReasons := map[autoClaimReason]string{
		reasonUnexpectedError:          "unexpected_error",
		reasonClaimed:                  "claimed",
		reasonDeadlineExceeded:         "deadline_exceeded",
		reasonCanceled:                 "canceled",
		reasonBeforeClaimWindow:        "before_claim_window",
		reasonSignedOut:                "signed_out",
		reasonAuthenticationFailed:     "authentication_failed",
		reasonIdentityUnavailable:      "identity_unavailable",
		reasonAlreadyAttempted:         "already_attempted",
		reasonMarkerFailed:             "marker_failed",
		reasonPreviewFailed:            "preview_failed",
		reasonAmbiguousOffer:           "ambiguous_offer",
		reasonNoEligibleOffer:          "no_eligible_offer",
		reasonCaptchaPreparationFailed: "captcha_preparation_failed",
		reasonClaimPreparationFailed:   "claim_preparation_failed",
		reasonAccountChanged:           "account_changed",
		reasonClaimRequestFailed:       "claim_request_failed",
		reasonClaimResponseFailed:      "claim_response_failed",
		reasonClaimResponseInvalid:     "claim_response_invalid",
	}
	for reason, want := range wantReasons {
		if got := schedulerReason(schedulerError(reason, errors.New("x"))); got != want {
			t.Errorf("schedulerReason(%d) = %q, want %q", int(reason), got, want)
		}
	}
	if got := schedulerReason(claimRejectedError(claimFailureRiskBlocked.String(), errors.New("x"))); got != "claim_risk_blocked" {
		t.Errorf("claim rejection reason = %q, want claim_risk_blocked", got)
	}
}

func TestClaimFailureStrings(t *testing.T) {
	for kind, want := range map[claimFailure]string{
		claimFailureNone: "", claimFailureNotFound: "not_found", claimFailureUnavailable: "unavailable",
		claimFailureAlreadyClaimed: "already_claimed", claimFailureIneligible: "ineligible",
		claimFailureQuotaExhausted: "quota_exhausted", claimFailureInvalidRequest: "invalid_request",
		claimFailureCaptcha: "captcha", claimFailureRiskBlocked: "risk_blocked",
		claimFailureLoginRequired: "login_required", claimFailureHTTPError: "http_error",
		claimFailureUnknown: "unknown",
	} {
		if got := kind.String(); got != want {
			t.Errorf("claimFailure(%d) = %q, want %q", int(kind), got, want)
		}
	}
	if got := ClaimPreflightFailure(schedulerError(reasonCaptchaPreparationFailed, errors.New("x"))); got != "captcha" {
		t.Errorf("preflight captcha = %q", got)
	}
	if got := ClaimPreflightFailure(schedulerError(reasonAuthenticationFailed, errors.New("x"))); got != "login_required" {
		t.Errorf("preflight login = %q", got)
	}
	if got := ClaimPreflightFailure(schedulerError(reasonClaimRequestFailed, errors.New("x"))); got != "" {
		t.Errorf("post-submission failure = %q, want empty", got)
	}
}

func TestAutoClaimMarkerState(t *testing.T) {
	path := t.TempDir() + "/auth.json.autoclaim/marker"
	if state, err := autoClaimMarker(path); state != autoClaimMarkerAbsent || err != nil {
		t.Fatalf("missing marker = %v, %v", state, err)
	}
	if err := createAutoClaimMarker(path); err != nil {
		t.Fatal(err)
	}
	if state, err := autoClaimMarker(path); state != autoClaimMarkerPresent || err != nil {
		t.Fatalf("created marker = %v, %v", state, err)
	}
}
