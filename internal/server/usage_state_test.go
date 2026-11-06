package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

func TestUsageSnapshotStateAt(t *testing.T) {
	now := time.Now()
	cases := []struct {
		present bool
		at      time.Time
		want    usageSnapshotState
	}{
		{present: false, at: now, want: usageSnapshotEmpty},
		{present: true, at: now, want: usageSnapshotFresh},
		{present: true, at: now.Add(-time.Minute), want: usageSnapshotExpired},
		{present: true, at: time.Time{}, want: usageSnapshotExpired},
	}
	for _, tc := range cases {
		if got := usageSnapshotStateAt(tc.present, tc.at, time.Minute, now); got != tc.want {
			t.Errorf("usageSnapshotStateAt(%v, %v) = %v, want %v", tc.present, tc.at, got, tc.want)
		}
	}
	if got := zcodeSnapshotState(time.Time{}, now); got != usageSnapshotEmpty {
		t.Errorf("zero ZCode timestamp = %v, want empty", got)
	}
}

func TestUsageAvailabilityJSON(t *testing.T) {
	cases := []struct {
		state  usageAvailability
		reason string
		want   string
	}{
		{usageUnconfigured, "ignored", `{"configured":false,"available":false}`},
		{usageUnavailable, "", `{"configured":true,"available":false}`},
		{usageUnavailable, "down", `{"configured":true,"available":false,"error":"down"}`},
		{usageAvailable, "ignored", `{"configured":true,"available":true}`},
	}
	for _, tc := range cases {
		meta := tc.state.metadata(tc.reason)
		body, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != tc.want {
			t.Errorf("%v.metadata(%q) = %s, want %s", tc.state, tc.reason, body, tc.want)
		}
		if got := meta.availability(); got != tc.state {
			t.Errorf("%v round-tripped to %v", tc.state, got)
		}
	}
}

func TestZcodeClaimResultStatus(t *testing.T) {
	outcomes := []struct {
		outcome zcodebackend.ClaimOutcome
		want    int
	}{
		{zcodebackend.ClaimOutcome{OK: true, FailureKind: "captcha"}, http.StatusOK},
		{zcodebackend.ClaimOutcome{FailureKind: "already_claimed"}, http.StatusOK},
		{zcodebackend.ClaimOutcome{FailureKind: "captcha"}, http.StatusBadRequest},
		{zcodebackend.ClaimOutcome{FailureKind: "invalid_request"}, http.StatusBadRequest},
		{zcodebackend.ClaimOutcome{FailureKind: "login_required"}, http.StatusUnauthorized},
		{zcodebackend.ClaimOutcome{FailureKind: "other"}, http.StatusConflict},
		{zcodebackend.ClaimOutcome{}, http.StatusConflict},
	}
	for _, tc := range outcomes {
		if got := zcodeOutcomeResult(tc.outcome).status(); got != tc.want {
			t.Errorf("outcome %+v status = %d, want %d", tc.outcome, got, tc.want)
		}
	}
	if got := zcodePreflightResult("login_required").status(); got != http.StatusUnauthorized {
		t.Errorf("login preflight status = %d", got)
	}
	if got := zcodePreflightResult("captcha").status(); got != http.StatusBadRequest {
		t.Errorf("captcha preflight status = %d", got)
	}
	if got := zcodeClaimUpstreamError.status(); got != http.StatusBadGateway {
		t.Errorf("upstream error status = %d", got)
	}
}

func TestClassifyMiniMaxUsage(t *testing.T) {
	plain := errors.New("down")
	dead := backend.Terminal(errors.New("signed out"))
	cases := []struct {
		account, checkin       bool
		accountErr, checkinErr error
		want                   minimaxUsageResult
		status                 int
	}{
		{true, true, nil, nil, minimaxUsageComplete, http.StatusOK},
		{true, false, nil, dead, minimaxUsagePartial, http.StatusOK},
		{false, true, dead, nil, minimaxUsagePartial, http.StatusOK},
		{false, false, plain, plain, minimaxUsageFailed, http.StatusBadGateway},
		{false, false, dead, plain, minimaxUsageFailed, http.StatusBadGateway},
		{false, false, dead, dead, minimaxUsageSessionExpired, http.StatusConflict},
	}
	for _, tc := range cases {
		got := classifyMiniMaxUsage(tc.account, tc.checkin, tc.accountErr, tc.checkinErr)
		if got != tc.want || got.status() != tc.status {
			t.Errorf("classify(%v,%v,%v,%v) = %v/%d, want %v/%d", tc.account, tc.checkin, tc.accountErr, tc.checkinErr, got, got.status(), tc.want, tc.status)
		}
	}
}

func TestLoginOutcomeTransitions(t *testing.T) {
	if loginWaiting.terminal() {
		t.Fatal("waiting must not be terminal")
	}
	for _, o := range []loginOutcome{loginSucceeded, loginFailed, loginTimedOut, loginClientGone} {
		if !o.terminal() {
			t.Errorf("%v must be terminal", o)
		}
	}
	if got := loginResultOutcome(nil); got != loginSucceeded {
		t.Errorf("nil result = %v", got)
	}
	if got := loginResultOutcome(errors.New("denied")); got != loginFailed {
		t.Errorf("error result = %v", got)
	}
}

type fakeAccountSession struct {
	backend.TokenSource
	signedIn bool
}

func (f fakeAccountSession) HasSession() bool { return f.signedIn }

func TestAccountSessionState(t *testing.T) {
	cases := []struct {
		provider accountProvider
		want     accountSessionState
	}{
		{accountProvider{}, accountAbsent},
		{accountProvider{session: fakeAccountSession{}}, accountSignedOut},
		{accountProvider{session: fakeAccountSession{signedIn: true}}, accountSignedIn},
	}
	for _, tc := range cases {
		if got := tc.provider.sessionState(); got != tc.want {
			t.Errorf("sessionState = %v, want %v", got, tc.want)
		}
		if tc.provider.configured() != (tc.want == accountSignedIn) {
			t.Errorf("configured mismatch for %v", tc.want)
		}
	}
}
