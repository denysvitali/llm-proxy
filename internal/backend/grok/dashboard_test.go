package grok

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagerUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			if got := r.URL.Query().Get("include"); got != "subscription" {
				t.Errorf("include = %q, want subscription", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":{"user":{"id":"user-1","email":"person@example.com","firstName":"Test","lastName":"User","subscription":{"tier":"super"}}}}`))
		case "/billing":
			if got := r.URL.Query().Get("format"); got != "credits" {
				t.Errorf("format = %q, want credits", got)
			}
			if got := r.Header.Get("X-Userid"); got != "user-1" {
				t.Errorf("x-userid = %q, want user-1", got)
			}
			_, _ = w.Write([]byte(`{"data":{"billing":{"credits":{"creditUsagePercent":42.5,"monthlyLimit":10000,"used":4250,"prepaidBalance":250,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_MONTHLY","start":"2026-08-01","end":"2026-09-01"}}}}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	path := t.TempDir() + "/auth.json"
	manager := NewManager(path)
	if err := manager.Store.Save(&Token{AccessToken: "test-token"}); err != nil {
		t.Fatalf("save token: %v", err)
	}

	usage, err := manager.Usage(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	view := NewUsageView(usage, time.Unix(0, 0).UTC())
	if !view.Available || !view.HasPercent || view.PercentUsed != 42.5 {
		t.Fatalf("usage view = %+v", view)
	}
	if view.Email != "person@example.com" || view.SubscriptionTier != "super" {
		t.Errorf("identity = %q %q", view.Email, view.SubscriptionTier)
	}
	if view.LimitCents == nil || *view.LimitCents != 10000 || view.UsedCents == nil || *view.UsedCents != 4250 {
		t.Errorf("credit amounts = %+v", view)
	}
	if view.RemainingCents == nil || *view.RemainingCents != 5750 {
		t.Errorf("remaining = %+v", view.RemainingCents)
	}
}
