package zcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPlanUsageSendsCurrentZCodeIdentity(t *testing.T) {
	t.Helper()
	const token = "test-zcode-jwt"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/zcode-plan/billing/current" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("app_version"); got != zcodeAppVersion {
			t.Errorf("app_version = %q, want %q", got, zcodeAppVersion)
		}
		wantHeaders := map[string]string{
			"Authorization":       "Bearer " + token,
			"Accept":              "application/json",
			"User-Agent":          "ZCode/" + zcodeAppVersion,
			"X-ZCode-App-Version": zcodeAppVersion,
			"X-Title":             "Z Code@electron",
			"HTTP-Referer":        "https://zcode.z.ai",
			"X-Platform":          runtime.GOOS + "-" + zcodeArch(),
			"X-Device-Mid":        deviceMID(token),
		}
		for name, want := range wantHeaders {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if got := r.Header.Get(aliyunCaptchaHeader); got != "" {
			t.Errorf("%s = %q, want omitted", aliyunCaptchaHeader, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[{"plan_id":"start-plan","status":"active","total_units":3000000,"used_units":1200000,"available_units":1800000,"period_start":1756684800,"period_end":1756771199}]}}`))
	}))
	defer upstream.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: token}); err != nil {
		t.Fatal(err)
	}
	plans, err := manager.PlanUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v", plans)
	}
	plan := plans[0]
	if plan.PlanID != "start-plan" || plan.Status != "active" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.TotalUnits != 3000000 || plan.UsedUnits != 1200000 || plan.AvailableUnits != 1800000 {
		t.Fatalf("units = %+v", plan)
	}
	if plan.PeriodStart != 1756684800 || plan.PeriodEnd != 1756771199 {
		t.Fatalf("period = %+v", plan)
	}
}

func TestPlanUsageAcceptsStringUnitValues(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[{"plan_id":"start-plan","total_units":"3000000","used_units":"500"}]}}`))
	}))
	defer upstream.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: "test-zcode-jwt"}); err != nil {
		t.Fatal(err)
	}
	plans, err := manager.PlanUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].TotalUnits != 3000000 || plans[0].UsedUnits != 500 {
		t.Fatalf("plans = %+v", plans)
	}
}

func TestPlanUsageSurfacesUnavailableEntitlement(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[{"plan_id":"start-plan","kind":"unavailable","reason":"coding_plan_not_entitled"}]}}`))
	}))
	defer upstream.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: "test-zcode-jwt"}); err != nil {
		t.Fatal(err)
	}
	plans, err := manager.PlanUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Kind != "unavailable" || plans[0].Reason != "coding_plan_not_entitled" {
		t.Fatalf("plans = %+v", plans)
	}
}

func TestPlanUsageRejectsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":401,"msg":"token expired"}`))
	}))
	defer upstream.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: "test-zcode-jwt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PlanUsage(context.Background()); err == nil {
		t.Fatal("PlanUsage() error = nil, want failure")
	}
}
