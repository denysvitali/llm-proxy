package zcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPlanUsageSendsBalanceZCodeIdentity(t *testing.T) {
	t.Helper()
	const previousToken = "previous-zcode-jwt"
	const token = "test-zcode-jwt"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/zcode-plan/billing/balance" {
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
			"X-Device-Mid":        deviceMID(previousToken),
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
	if err := manager.Store.Save(&Credentials{AccessToken: previousToken}); err != nil {
		t.Fatal(err)
	}
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

func TestPlanQuotaSendsBalanceRequestAndNormalizesBuckets(t *testing.T) {
	t.Helper()
	const token = "test-zcode-jwt"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/zcode-plan/billing/balance" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("app_version"); got != zcodeAppVersion {
			t.Errorf("app_version = %q, want %q", got, zcodeAppVersion)
		}
		for name, want := range map[string]string{
			"Authorization":       "Bearer " + token,
			"Accept":              "application/json",
			"User-Agent":          "ZCode/" + zcodeAppVersion,
			"X-ZCode-App-Version": zcodeAppVersion,
			"X-Device-Mid":        deviceMID(token),
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[{"plan_id":"start-plan","name":"Start Plan","status":"active"}],"balances":[{"entitlement_id":"glm-flash","plan_id":"start-plan","show_name":"GLM Flash","meter":"model_usage","capabilities":["model:glm-5.3-flash"],"total_units":"3000000","used_units":1200000,"remaining_units":"1800000","available_units":1800000,"reserved_units":"0","expires_at":"1756771199"}]}}`))
	}))
	defer upstream.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: token}); err != nil {
		t.Fatal(err)
	}
	quota, err := manager.PlanQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(quota.Plans) != 1 || quota.Plans[0].PlanID != "start-plan" {
		t.Fatalf("plans = %+v", quota.Plans)
	}
	if len(quota.Balances) != 1 {
		t.Fatalf("balances = %+v", quota.Balances)
	}
	balance := quota.Balances[0]
	if balance.EntitlementID != "glm-flash" || balance.PlanID != "start-plan" || balance.Meter != "model_usage" {
		t.Fatalf("balance identity = %+v", balance)
	}
	if balance.TotalUnits != 3000000 || balance.UsedUnits != 1200000 || balance.RemainingUnits != 1800000 || balance.AvailableUnits != 1800000 || balance.ReservedUnits != 0 {
		t.Fatalf("balance units = %+v", balance)
	}
	if balance.ExpiresAt != 1756771199 || len(balance.Capabilities) != 1 || balance.Capabilities[0] != "model:glm-5.3-flash" {
		t.Fatalf("balance expiry/capabilities = %+v", balance)
	}
}

func TestPlanBalanceJSONPreservesCounterPresence(t *testing.T) {
	fields := []string{"total_units", "used_units", "remaining_units", "available_units", "reserved_units"}
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "absent"},
		{name: "null", value: "null"},
		{name: "numeric zero", value: "0", want: "0"},
		{name: "string zero", value: `"0"`, want: "0"},
		{name: "numeric nonzero", value: "25.5", want: "25.5"},
		{name: "string nonzero", value: `"25.5"`, want: "25.5"},
	} {
		for _, reportedField := range fields {
			t.Run(tc.name+"/"+reportedField, func(t *testing.T) {
				raw := make(map[string]json.RawMessage)
				if tc.value != "" {
					raw[reportedField] = json.RawMessage(tc.value)
				}
				input, err := json.Marshal([]any{raw})
				if err != nil {
					t.Fatal(err)
				}
				var payload []planBalancePayload
				if err := json.Unmarshal(input, &payload); err != nil {
					t.Fatal(err)
				}
				balances := normalizePlanBalances(payload)
				// The usage cache clones balances by value. Changing the source
				// after copying must preserve both the copy's counters and presence.
				copied := balances[0]
				balances[0].UsedUnits = 13
				output, err := json.Marshal(copied)
				if err != nil {
					t.Fatal(err)
				}
				var result map[string]json.RawMessage
				if err := json.Unmarshal(output, &result); err != nil {
					t.Fatal(err)
				}
				for _, field := range fields {
					want := ""
					if field == reportedField {
						want = tc.want
					}
					if got := string(result[field]); got != want {
						t.Errorf("%s = %q, want %q; JSON: %s", field, got, want, output)
					}
				}
			})
		}
	}
}

func TestPlanBalanceJSONKeepsCallerProvidedCounters(t *testing.T) {
	output, err := json.Marshal(PlanBalance{RemainingUnits: 123})
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"remaining_units":123}` {
		t.Fatalf("JSON = %s", output)
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
