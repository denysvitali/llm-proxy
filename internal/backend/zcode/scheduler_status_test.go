package zcode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestAutoClaimStatusReasonsAndTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name, preview, claim, reason, outcome string
		noCaptcha, attempted, success         bool
	}{
		{name: "success", claim: `{"code":0}`, reason: "claimed", outcome: "success", attempted: true, success: true},
		{name: "no offer", preview: `{"code":0,"data":{"plans":[]}}`, reason: "no_eligible_offer", outcome: "unavailable"},
		{name: "preview failure", preview: `private-upstream-data`, reason: "preview_failed", outcome: "failed"},
		{name: "captcha preparation", noCaptcha: true, reason: "captcha_preparation_failed", outcome: "failed"},
		{name: "captcha rejected", claim: `{"code":3007,"msg":"private-upstream-data"}`, reason: "claim_captcha", outcome: "failed", attempted: true},
		{name: "uncertain response", claim: `private-upstream-data`, reason: "claim_response_invalid", outcome: "failed", attempted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if tc.preview == "" {
						writeClaimPreview(w, claimTestTime())
					} else {
						_, _ = io.WriteString(w, tc.preview)
					}
					return
				}
				_, _ = io.WriteString(w, tc.claim)
			})
			if tc.noCaptcha {
				m.InvalidateCaptcha("test-proof")
			}
			if status := m.AutoClaimStatus(); status.LastCheckUnix != 0 || status.LastAttemptUnix != 0 || status.LastSuccessUnix != 0 || len(status.ReasonCounts) != 0 {
				t.Fatalf("new manager has observations: %+v", status)
			}
			var logs bytes.Buffer
			log := logrus.New()
			log.SetOutput(&logs)
			logged := make(chan struct{}, 1)
			log.AddHook(claimLogHook{logged})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); m.runAutoClaim(ctx, log, time.Hour, claimTestTime) }()
			select {
			case <-logged:
			case <-time.After(3 * time.Second):
				t.Fatal("scheduler did not report an outcome")
			}
			cancel()
			<-done
			status := m.AutoClaimStatus()
			if status.LastReason != tc.reason || status.LastOutcome != tc.outcome || status.ReasonCounts[tc.reason] != 1 || status.LastCheckUnix == 0 || (status.LastAttemptUnix > 0) != tc.attempted || (status.LastSuccessUnix > 0) != tc.success {
				t.Fatalf("status = %+v", status)
			}
			if !strings.Contains(logs.String(), "reason="+tc.reason) || strings.Contains(logs.String(), "private-upstream-data") || strings.Contains(logs.String(), "account-a") || strings.Contains(logs.String(), "test-proof") {
				t.Fatalf("unsafe or missing reason: %s", logs.String())
			}
			status.ReasonCounts[tc.reason] = 100
			if m.AutoClaimStatus().ReasonCounts[tc.reason] != 1 {
				t.Fatal("snapshot mutation changed manager counters")
			}
			if tc.attempted {
				if err := m.autoClaim(context.Background(), claimTestTime()); schedulerReason(err) != "already_attempted" {
					t.Fatalf("durable suppression reason = %v", err)
				}
			}
		})
	}
}

func TestAutoClaimReasonsForLocalPreconditions(t *testing.T) {
	m := autoClaimTestManager(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream request") })
	if err := m.autoClaim(context.Background(), claimTestTime().Add(-time.Hour)); !errors.Is(err, errAutoClaimUnavailable) || schedulerReason(err) != "before_claim_window" {
		t.Fatalf("before window: %v", err)
	}
	if err := os.Remove(m.Store.Path); err != nil {
		t.Fatal(err)
	}
	if err := m.autoClaim(context.Background(), claimTestTime()); !errors.Is(err, errAutoClaimUnavailable) || schedulerReason(err) != "signed_out" {
		t.Fatalf("signed out: %v", err)
	}
}

func TestAutoClaimStatusConcurrentObservations(t *testing.T) {
	m := &Manager{}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				m.noteSchedulerAttempt()
				m.noteSchedulerResult("success", "claimed")
				m.AutoClaimStatus().ReasonCounts["claimed"] = 0
			}
		}()
	}
	workers.Wait()
	if got := m.AutoClaimStatus().ReasonCounts["claimed"]; got != 800 {
		t.Fatalf("claimed count = %d, want 800", got)
	}
}
