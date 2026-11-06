package grok

import (
	"errors"
	"testing"
	"time"
)

func TestClassifySession(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name  string
		token *Token
		want  sessionState
	}{
		{"no file", nil, sessionUnsaved},
		{"empty token", &Token{}, sessionSignedOut},
		{"no expiry", &Token{AccessToken: "a"}, sessionActive},
		{"far expiry", &Token{AccessToken: "a", ExpiresAt: float64(now.Add(time.Hour).Unix())}, sessionActive},
		{"inside window", &Token{AccessToken: "a", ExpiresAt: float64(now.Add(time.Minute).Unix())}, sessionExpiring},
	} {
		if got := classifySession(tc.token, now); got != tc.want {
			t.Errorf("%s: classifySession = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClassifyDevicePoll(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want devicePoll
	}{
		{nil, deviceAuthorized},
		{errors.New(`HTTP 400 from x: {"error":"authorization_pending"}`), devicePending},
		{errors.New(`HTTP 400 from x: {"error":"slow_down"}`), deviceSlowDown},
		{errors.New(`HTTP 400 from x: {"error":"access_denied"}`), deviceFailed},
	} {
		if got := classifyDevicePoll(tc.err); got != tc.want {
			t.Errorf("classifyDevicePoll(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
