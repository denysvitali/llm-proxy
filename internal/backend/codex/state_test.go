package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestClassifySession(t *testing.T) {
	soon := testJWT(t, map[string]any{"exp": time.Now().Add(time.Minute).Unix()})
	later := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	for _, tc := range []struct {
		name        string
		credentials *Credentials
		want        sessionState
	}{
		{"missing", nil, sessionSignedOut},
		{"empty token", &Credentials{}, sessionSignedOut},
		{"opaque token", &Credentials{AccessToken: "opaque"}, sessionActive},
		{"jwt far from expiry", &Credentials{AccessToken: later}, sessionActive},
		{"jwt inside window", &Credentials{AccessToken: soon}, sessionExpiring},
		{"explicit expiry", &Credentials{AccessToken: "opaque", ExpiresAt: time.Now().Unix()}, sessionExpiring},
	} {
		if got := classifySession(tc.credentials); got != tc.want {
			t.Errorf("%s: classifySession = %v, want %v", tc.name, got, tc.want)
		}
	}
	filled := &Credentials{AccessToken: later}
	classifySession(filled)
	if filled.ExpiresAt == 0 {
		t.Fatalf("classifySession did not fill ExpiresAt from the JWT")
	}
}

func TestClassifyRejectedSession(t *testing.T) {
	if got := classifyRejectedSession(nil, "a"); got != sessionSignedOut {
		t.Fatalf("nil = %v", got)
	}
	if got := classifyRejectedSession(&Credentials{AccessToken: "a"}, "a"); got != sessionRejected {
		t.Fatalf("same token = %v", got)
	}
	if got := classifyRejectedSession(&Credentials{AccessToken: "b"}, "a"); got != sessionActive {
		t.Fatalf("replaced token = %v", got)
	}
}

func TestClassifyDevicePoll(t *testing.T) {
	failure := errors.New("HTTP")
	for _, tc := range []struct {
		status int
		err    error
		want   devicePoll
	}{
		{http.StatusOK, nil, deviceAuthorized},
		{http.StatusForbidden, failure, devicePending},
		{http.StatusNotFound, failure, devicePending},
		{http.StatusBadRequest, failure, deviceFailed},
		{0, failure, deviceFailed},
	} {
		if got := classifyDevicePoll(tc.status, tc.err); got != tc.want {
			t.Errorf("classifyDevicePoll(%d, %v) = %v, want %v", tc.status, tc.err, got, tc.want)
		}
	}
}

func TestCacheStateAt(t *testing.T) {
	now := time.Now()
	if got := cacheStateAt(false, now.Add(time.Hour), now); got != cacheEmpty {
		t.Fatalf("absent = %v", got)
	}
	if got := cacheStateAt(true, now.Add(time.Second), now); got != cacheFresh {
		t.Fatalf("before deadline = %v", got)
	}
	if got := cacheStateAt(true, now, now); got != cacheStale {
		t.Fatalf("at deadline = %v", got)
	}
}

func TestUnauthorizedRetryString(t *testing.T) {
	if retryFirstAttempt.String() != "first_attempt" || retryRefreshed.String() != "refreshed" {
		t.Fatalf("unexpected String values")
	}
}

type forceRefresher struct {
	staticCredentials
	refreshes int
	err       error
}

func (f *forceRefresher) ForceRefresh(_ context.Context, stale string) (Credentials, error) {
	f.refreshes++
	if f.err != nil {
		return Credentials{}, f.err
	}
	return Credentials{AccessToken: stale + "-fresh"}, nil
}

func TestSendWithRefreshRefreshesOnce(t *testing.T) {
	respond := func(status int) *http.Response {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}
	}
	for _, tc := range []struct {
		name      string
		statuses  []int
		tokens    backend.TokenSource
		refreshes int
		sends     int
		final     int
		wantErr   bool
	}{
		{"ok", []int{200}, &forceRefresher{}, 0, 1, 200, false},
		{"refreshed", []int{401, 200}, &forceRefresher{}, 1, 2, 200, false},
		{"second 401 relayed", []int{401, 401}, &forceRefresher{}, 1, 2, 401, false},
		{"cannot refresh", []int{401}, staticCredentials{}, 0, 1, 401, false},
		{"refresh fails", []int{401}, &forceRefresher{err: errors.New("nope")}, 1, 1, 0, true},
	} {
		c := &Client{Tokens: tc.tokens}
		var sent []string
		resp, err := c.sendWithRefresh(t.Context(), Credentials{AccessToken: "a"}, func(credentials Credentials) (*http.Response, error) {
			sent = append(sent, credentials.AccessToken)
			return respond(tc.statuses[len(sent)-1]), nil
		}, func(err error) error { return fmt.Errorf("wrapped: %w", err) })
		if (err != nil) != tc.wantErr || (err != nil && !strings.HasPrefix(err.Error(), "wrapped: ")) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if len(sent) != tc.sends || (len(sent) == 2 && sent[1] != "a-fresh") {
			t.Errorf("%s: sends = %v", tc.name, sent)
		}
		if f, ok := tc.tokens.(*forceRefresher); ok && f.refreshes != tc.refreshes {
			t.Errorf("%s: refreshes = %d, want %d", tc.name, f.refreshes, tc.refreshes)
		}
		if resp != nil && resp.StatusCode != tc.final {
			t.Errorf("%s: final status = %d, want %d", tc.name, resp.StatusCode, tc.final)
		}
	}
}
