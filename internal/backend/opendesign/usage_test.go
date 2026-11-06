package opendesign

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const fundingCheckBody = `{"schemaVersion":1,"workspaceId":"%s","workspaceMemberId":"m1","modelId":null,
"generatedAt":"2026-11-06T12:00:00Z","balanceUsd":"3.50","funding":"coding_plan","fundingCheck":"available","modelCovered":null,
"codingPlan":{"workspaceId":"%[1]s","generatedAt":"2026-11-06T12:00:00Z","eligible":true,"tier":"pro","windows":[
{"policyId":"p5h","durationSeconds":18000,"resetMode":"activity_triggered","usedCredits":"12500","remainingCredits":"37500","limitCredits":"50000","windowStart":"2026-11-06T10:00:00Z","resetsAt":"2026-11-06T15:00:00Z"},
{"policyId":"p7d","durationSeconds":604800,"resetMode":"anchored_recurring","usedCredits":"150445","remainingCredits":"0","limitCredits":"150000","windowStart":null,"resetsAt":null}]}}`

func accountAPI(t *testing.T, directory string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ctl" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"error":"unauthenticated"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/workspaces":
			calls.Add(1)
			_, _ = fmt.Fprint(w, directory)
		case strings.HasPrefix(r.URL.Path, "/api/v1/workspaces/") && strings.HasSuffix(r.URL.Path, "/billing/funding-check"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/"), "/billing/funding-check")
			_, _ = fmt.Fprintf(w, fundingCheckBody, id)
		case r.URL.Path == "/api/v1/billing/summary":
			_, _ = fmt.Fprint(w, `{"membershipTier":"pro","creditsPerUsd":10000}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestUsageDiscoversDefaultWorkspace(t *testing.T) {
	var calls atomic.Int32
	server := accountAPI(t, `{"items":[
{"workspaceId":"team1","workspaceType":"team","memberStatus":"active","lifecycleState":"active"},
{"workspaceId":"ws-default","workspaceType":"personal","memberStatus":"active","lifecycleState":"active","isDefault":true}]}`, &calls)
	defer server.Close()

	client := NewUsageClient(server.URL, "ctl", "")
	for range 2 {
		usage, err := client.Usage(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if usage.WorkspaceID != "ws-default" || usage.Tier != "pro" || !usage.Eligible || usage.BalanceUSD != "3.50" || usage.CreditsPerUSD != 10000 {
			t.Fatalf("usage = %+v", usage)
		}
		if len(usage.Windows) != 2 {
			t.Fatalf("windows = %+v", usage.Windows)
		}
		five, week := usage.Windows[0], usage.Windows[1]
		if five.DurationSeconds != 18000 || five.UsedCredits != 12500 || five.LimitCredits != 50000 || five.ResetsAt == nil {
			t.Errorf("5h window = %+v", five)
		}
		// Overuse (used > limit) is real data, not malformed.
		if week.UsedCredits != 150445 || week.RemainingCredits != 0 || week.ResetsAt != nil {
			t.Errorf("weekly window = %+v", week)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("workspace directory fetched %d times, want once", calls.Load())
	}
}

func TestUsageWorkspaceSelection(t *testing.T) {
	for _, tc := range []struct {
		name, directory, configured, want, wantErr string
	}{
		{name: "configured skips discovery", directory: `{"items":[]}`, configured: "pinned", want: "pinned"},
		{name: "single active", directory: `{"items":[{"workspaceId":"only","workspaceType":"team","memberStatus":"active"},{"workspaceId":"gone","workspaceType":"personal","memberStatus":"removed"}]}`, want: "only"},
		{name: "single personal among teams", directory: `{"items":[{"workspaceId":"t1","workspaceType":"team","memberStatus":"active"},{"workspaceId":"me","workspaceType":"personal","memberStatus":"active"},{"workspaceId":"t2","workspaceType":"team","memberStatus":"active"}]}`, want: "me"},
		{name: "ambiguous", directory: `{"items":[{"workspaceId":"t1","workspaceType":"team","memberStatus":"active"},{"workspaceId":"t2","workspaceType":"team","memberStatus":"active"}]}`, wantErr: "set workspace_id"},
		{name: "none", directory: `{"items":[]}`, wantErr: "no active workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := accountAPI(t, tc.directory, &calls)
			defer server.Close()
			usage, err := NewUsageClient(server.URL, "ctl", tc.configured).Usage(context.Background())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || usage.WorkspaceID != tc.want {
				t.Fatalf("usage = %+v, err = %v, want workspace %q", usage, err, tc.want)
			}
		})
	}
}

func TestUsageRejectedKey(t *testing.T) {
	var calls atomic.Int32
	server := accountAPI(t, `{"items":[]}`, &calls)
	defer server.Close()
	_, err := NewUsageClient(server.URL, "wrong", "ws").Usage(context.Background())
	if !errors.Is(err, ErrUsageUnauthorized) {
		t.Fatalf("err = %v, want ErrUsageUnauthorized", err)
	}
}

func TestUsageRejectsMalformedPlan(t *testing.T) {
	for name, body := range map[string]string{
		"schema":    `{"schemaVersion":2,"workspaceId":"ws","codingPlan":{"windows":[]}}`,
		"workspace": `{"schemaVersion":1,"workspaceId":"other","codingPlan":{"windows":[]}}`,
		"no plan":   `{"schemaVersion":1,"workspaceId":"ws"}`,
		"credits":   `{"schemaVersion":1,"workspaceId":"ws","codingPlan":{"windows":[{"policyId":"p","durationSeconds":60,"usedCredits":"1.5","remainingCredits":"1","limitCredits":"2"}]}}`,
		"limit":     `{"schemaVersion":1,"workspaceId":"ws","codingPlan":{"windows":[{"policyId":"p","durationSeconds":60,"usedCredits":"0","remainingCredits":"0","limitCredits":"0"}]}}`,
		"remaining": `{"schemaVersion":1,"workspaceId":"ws","codingPlan":{"windows":[{"policyId":"p","durationSeconds":60,"usedCredits":"0","remainingCredits":"3","limitCredits":"2"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprint(w, body)
			}))
			defer server.Close()
			if _, err := NewUsageClient(server.URL, "ctl", "ws").Usage(context.Background()); err == nil {
				t.Fatal("malformed funding-check accepted")
			}
		})
	}
}

func TestUsageWithoutRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/billing/summary" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintf(w, fundingCheckBody, "ws")
	}))
	defer server.Close()
	usage, err := NewUsageClient(server.URL, "ctl", "ws").Usage(context.Background())
	if err != nil || usage.CreditsPerUSD != 0 || len(usage.Windows) != 2 {
		t.Fatalf("summary failure must only drop the rate: %+v, %v", usage, err)
	}
}
