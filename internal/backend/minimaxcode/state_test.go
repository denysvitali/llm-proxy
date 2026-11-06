package minimaxcode

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassifySession(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		credentials *Credentials
		want        sessionState
	}{
		{nil, sessionSignedOut},
		{&Credentials{}, sessionSignedOut},
		{&Credentials{AccessToken: "a"}, sessionActive},
		{&Credentials{AccessToken: "a", ExpiresAt: now.Add(time.Hour).Unix()}, sessionActive},
		{&Credentials{AccessToken: "a", ExpiresAt: now.Add(time.Minute).Unix()}, sessionExpiring},
	} {
		if got := classifySession(tc.credentials, now); got != tc.want {
			t.Errorf("classifySession(%+v) = %v, want %v", tc.credentials, got, tc.want)
		}
	}
}

func TestDetectDeviceProtocol(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Minute).UnixMilli()
	for _, tc := range []struct {
		device deviceAuthorization
		want   deviceProtocol
	}{
		{deviceAuthorization{DeviceCode: "d", UserCode: "u"}, deviceCodeProtocol},
		{deviceAuthorization{UserCode: "u", ExpiredIn: future}, legacyUserCodeProtocol},
		{deviceAuthorization{UserCode: "u", ExpiredIn: now.Add(-time.Minute).UnixMilli()}, deviceCodeProtocol},
		{deviceAuthorization{DeviceCode: "d", UserCode: "u", ExpiredIn: future}, deviceCodeProtocol},
	} {
		if got := detectDeviceProtocol(tc.device, now); got != tc.want {
			t.Errorf("detectDeviceProtocol(%+v) = %v, want %v", tc.device, got, tc.want)
		}
	}
}

func TestClassifyDevicePoll(t *testing.T) {
	for _, tc := range []struct {
		status string
		err    error
		want   devicePoll
	}{
		{"pending", nil, devicePending},
		{"slow_down", nil, deviceSlowDown},
		{"denied", nil, deviceRejected},
		{"access_denied", nil, deviceRejected},
		{"expired", nil, deviceRejected},
		{"expired_token", nil, deviceRejected},
		{"", nil, deviceAuthorized},
		{"", oauthError("authorization_pending"), devicePending},
		{"", oauthError("slow_down"), deviceSlowDown},
		{"", oauthError("access_denied"), deviceFailed},
		{"", errors.New("HTTP 500"), deviceFailed},
	} {
		if got := classifyDevicePoll(tc.status, tc.err); got != tc.want {
			t.Errorf("classifyDevicePoll(%q, %v) = %v, want %v", tc.status, tc.err, got, tc.want)
		}
	}
}

func TestPanelEligibility(t *testing.T) {
	day := func(status checkinDayStatus, today bool) CheckinDay {
		return CheckinDay{Status: int(status), IsToday: today}
	}
	for _, tc := range []struct {
		days []CheckinDay
		want checkinEligibility
	}{
		{[]CheckinDay{day(dayUpcoming, false), day(dayUnavailable, true)}, checkinUnavailable},
		{[]CheckinDay{day(dayAvailable, true)}, checkinClaimable},
		{[]CheckinDay{day(dayClaimed, true)}, checkinAlreadyClaimed},
		{[]CheckinDay{day(dayAvailable, false), day(dayClaimed, true)}, checkinAlreadyClaimed},
		{[]CheckinDay{day(dayClaimed, false), day(dayUpcoming, true)}, checkinUnavailable},
	} {
		if got := (&CheckinPanel{Days: tc.days}).eligibility(); got != tc.want {
			t.Errorf("eligibility(%+v) = %v, want %v", tc.days, got, tc.want)
		}
	}
}

func TestClassifyCheckinResult(t *testing.T) {
	live := context.Background()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx     context.Context
		err     error
		outcome string
		reason  string
		record  bool
	}{
		{live, nil, "success", "claimed", true},
		{live, fmt.Errorf("x: %w", ErrAutoCheckinAlreadyAttempted), "already_claimed", "already_attempted", true},
		{live, ErrCheckinAlreadyClaimed, "already_claimed", "already_claimed", true},
		{live, ErrCheckinUnavailable, "unavailable", "no_reward_available", true},
		{live, ErrCheckinInProgress, "skipped", "claim_in_progress", true},
		{live, schedulerError("account_changed", errors.New("x")), "failed", "account_changed", true},
		{live, context.Canceled, "failed", "canceled", true},
		{ended, context.Canceled, "", "", false},
	} {
		outcome, reason, record := classifyCheckinResult(tc.ctx, tc.err)
		if record != tc.record || (record && (outcome.String() != tc.outcome || reason != tc.reason)) {
			t.Errorf("classifyCheckinResult(%v) = %v, %q, %v; want %q, %q, %v", tc.err, outcome, reason, record, tc.outcome, tc.reason, tc.record)
		}
	}
}
