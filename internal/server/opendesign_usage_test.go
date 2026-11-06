package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	opendesignbackend "github.com/denysvitali/llm-proxy/internal/backend/opendesign"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestOpenDesignUsageEndpoint(t *testing.T) {
	resets := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	fetches := 0
	s := newTestServer(t, nil)
	s.openDesignUsage = &openDesignUsageCache{fetch: func(context.Context) (opendesignbackend.Usage, error) {
		fetches++
		return opendesignbackend.Usage{
			WorkspaceID: "ws", Tier: "pro", Eligible: true, Funding: "coding_plan", BalanceUSD: "3.50", CreditsPerUSD: 10000,
			Windows: []opendesignbackend.PlanWindow{
				{PolicyID: "p5h", DurationSeconds: 18000, UsedCredits: 12500, RemainingCredits: 37500, LimitCredits: 50000, ResetsAt: &resets},
				{PolicyID: "p7d", DurationSeconds: 604800, UsedCredits: 150445, RemainingCredits: 0, LimitCredits: 150000},
			},
		}, nil
	}}

	var view openDesignUsageView
	for range 2 {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/opendesign/usage", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
	}
	if fetches != 1 {
		t.Errorf("fetches = %d, want 1 within the TTL", fetches)
	}
	if view.Tier != "pro" || len(view.Windows) != 2 {
		t.Fatalf("view = %+v", view)
	}
	five, week := view.Windows[0], view.Windows[1]
	if five.Label != "5-hour" || five.UsedPercent != 25 || five.RemainingPercent != 75 || five.LimitUSD == nil || *five.LimitUSD != 5 || five.ResetsAt == nil || !five.ResetsAt.Equal(resets) {
		t.Errorf("5-hour window = %+v", five)
	}
	if week.Label != "Weekly" || week.UsedPercent <= 100 || week.RemainingPercent != 0 || week.UsedUSD == nil || *week.UsedUSD != 15.0445 {
		t.Errorf("weekly window = %+v (overuse must stay above 100%%)", week)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if !contains(rec.Body.String(), `"openDesignUsage":{"configured":true,"available":true}`) {
		t.Errorf("overview missing usage metadata: %s", rec.Body.String())
	}
}

func TestOpenDesignUsageErrors(t *testing.T) {
	s := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/opendesign/usage", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if !contains(rec.Body.String(), `"openDesignUsage":{"configured":false,"available":false}`) {
		t.Errorf("overview should report usage unconfigured: %s", rec.Body.String())
	}

	for name, fetchErr := range map[string]error{
		"unauthorized": opendesignbackend.ErrUsageUnauthorized,
		"upstream":     errors.New("open design account API: connection refused"),
	} {
		t.Run(name, func(t *testing.T) {
			s.openDesignUsage = &openDesignUsageCache{fetch: func(context.Context) (opendesignbackend.Usage, error) {
				return opendesignbackend.Usage{}, fetchErr
			}}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/opendesign/usage", nil))
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOpenDesignUsageCacheConfig(t *testing.T) {
	t.Setenv("OD_CONTROL_KEY", "ctl")
	enabled, disabled := true, false
	for _, tc := range []struct {
		name string
		cfg  config.BackendConfig
		want bool
	}{
		{"configured", config.BackendConfig{Type: "opendesign", UsageKeyEnv: "OD_CONTROL_KEY"}, true},
		{"no usage key", config.BackendConfig{Type: "opendesign"}, false},
		{"empty env", config.BackendConfig{Type: "opendesign", UsageKeyEnv: "OD_MISSING_KEY"}, false},
		{"disabled", config.BackendConfig{Type: "opendesign", UsageKeyEnv: "OD_CONTROL_KEY", Enabled: &disabled}, false},
		{"other backend", config.BackendConfig{Type: "venice", UsageKeyEnv: "OD_CONTROL_KEY", Enabled: &enabled}, false},
	} {
		if got := newOpenDesignUsageCache([]config.BackendConfig{tc.cfg}) != nil; got != tc.want {
			t.Errorf("%s: configured = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWindowLabel(t *testing.T) {
	for seconds, want := range map[int64]string{18000: "5-hour", 604800: "Weekly", 86400: "Daily", 30 * 86400: "30-day", 3600: "1-hour", 90: "2-minute"} {
		if got := windowLabel(seconds); got != want {
			t.Errorf("windowLabel(%d) = %q, want %q", seconds, got, want)
		}
	}
}
