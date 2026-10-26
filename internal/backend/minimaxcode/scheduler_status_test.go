package minimaxcode

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestAutoCheckinStatusReasonsAndTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name, reason, outcome         string
		panel                         func() map[string]any
		status                        int
		signedOut, attempted, success bool
	}{
		{name: "success", reason: "claimed", outcome: "success", panel: activePanel, status: 200, attempted: true, success: true},
		{name: "already claimed", reason: "already_claimed", outcome: "already_claimed", panel: claimedPanel, status: 200},
		{name: "no reward", reason: "no_reward_available", outcome: "unavailable", panel: func() map[string]any {
			p := testPanel()
			p["days"].([]any)[0].(map[string]any)["status"] = 1
			return p
		}, status: 200},
		{name: "signed out", reason: "signed_out", outcome: "skipped", panel: activePanel, status: 200, signedOut: true},
		{name: "unauthorized", reason: "authentication_failed", outcome: "failed", panel: activePanel, status: 401, attempted: true},
		{name: "rate limited", reason: "rate_limited", outcome: "failed", panel: activePanel, status: 429, attempted: true},
		{name: "uncertain upstream response", reason: "upstream_unavailable", outcome: "failed", panel: activePanel, status: 502, attempted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, posts, _ := autoCheckinManager(t, tc.panel, tc.status)
			if tc.signedOut {
				if err := os.Remove(m.Store.Path); err != nil {
					t.Fatal(err)
				}
			}
			if status := m.AutoCheckinStatus(); status.LastCheckUnix != 0 || status.LastAttemptUnix != 0 || status.LastSuccessUnix != 0 || len(status.ReasonCounts) != 0 {
				t.Fatalf("new manager has observations: %+v", status)
			}
			var logs synchronizedBuffer
			log := logrus.New()
			log.SetOutput(&logs)
			runCheckinUntilOutcome(t, m, time.Hour, tc.outcome, log)
			status := m.AutoCheckinStatus()
			if status.LastReason != tc.reason || status.LastOutcome != tc.outcome || status.ReasonCounts[tc.reason] != 1 || status.LastCheckUnix == 0 || (status.LastAttemptUnix > 0) != tc.attempted || (status.LastSuccessUnix > 0) != tc.success {
				t.Fatalf("status = %+v", status)
			}
			if !strings.Contains(logs.String(), "reason="+tc.reason) || strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "secret-account-token") || strings.Contains(logs.String(), "personal-user") {
				t.Fatalf("unsafe or missing reason: %s", logs.String())
			}
			status.ReasonCounts[tc.reason] = 100
			if m.AutoCheckinStatus().ReasonCounts[tc.reason] != 1 {
				t.Fatal("snapshot mutation changed manager counters")
			}
			if tc.attempted {
				// A fresh manager has empty counters but retains distributed suppression.
				restarted := NewManager(m.Store.Path)
				restarted.AccountBaseURL = m.AccountBaseURL
				if restarted.AutoCheckinStatus().LastAttemptUnix != 0 {
					t.Fatal("new manager inherited local observations")
				}
				quiet := logrus.New()
				quiet.SetOutput(io.Discard)
				runCheckinUntilOutcome(t, restarted, time.Hour, "already_claimed", quiet)
				if got := restarted.AutoCheckinStatus(); got.LastReason != "already_attempted" || got.LastAttemptUnix != 0 || got.LastSuccessUnix != 0 || posts.Load() != 1 {
					t.Fatalf("restart status = %+v, posts = %d", got, posts.Load())
				}
			}
		})
	}
}

func TestAutoCheckinStatusConcurrentObservations(t *testing.T) {
	m := &Manager{}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				m.noteSchedulerAttempt()
				m.noteSchedulerResult("success", "claimed")
				m.AutoCheckinStatus().ReasonCounts["claimed"] = 0
			}
		}()
	}
	workers.Wait()
	if got := m.AutoCheckinStatus().ReasonCounts["claimed"]; got != 800 {
		t.Fatalf("claimed count = %d, want 800", got)
	}
}
