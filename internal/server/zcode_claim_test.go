package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestZCodeOffersEndpointListsPlans(t *testing.T) {
	isolatePrometheus(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[{"plan_id":"weekend-free-1024","name":"Weekend Free"}]}}`))
	}))
	defer upstream.Close()

	manager := zcodebackend.NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&zcodebackend.Credentials{AccessToken: "test-zcode-jwt"}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:8090"}}
	s := NewWithDependencies(Dependencies{Config: cfg, Logger: quietLogger(), Accounts: AccountProviders{ZCode: manager}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/zcode/offers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"plan_id":"weekend-free-1024"`) {
		t.Errorf("body does not list the offer: %s", rec.Body.String())
	}
}

func TestZCodeClaimEndpointReportsSuccessAndRiskBlock(t *testing.T) {
	for _, tt := range []struct {
		name           string
		upstreamStatus int
		body           string
		wantStatus     int
		wantBody       string
	}{
		{"claimed", http.StatusOK, `{"code":0,"data":{"plan":{"starts_at":123,"ends_at":456}}}`, http.StatusOK, `"ok":true`},
		{"risk blocked", http.StatusForbidden, `{"code":3012,"msg":"request has been blocked due to unusual activity."}`, http.StatusConflict, `"failure_kind":"risk_blocked"`},
		{"malformed result", http.StatusOK, `{}`, http.StatusConflict, `"ok":false`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolatePrometheus(t)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/zcode-plan/billing/claim" {
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tt.upstreamStatus)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer upstream.Close()
			manager := zcodebackend.NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
			manager.CaptchaSolverURL = ""
			manager.Issuer = upstream.URL
			manager.HTTPClient = upstream.Client()
			if err := manager.Store.Save(&zcodebackend.Credentials{AccessToken: "test-zcode-jwt"}); err != nil {
				t.Fatal(err)
			}
			if err := manager.SetCaptchaVerifyParam("test-proof"); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:8090"}}
			s := NewWithDependencies(Dependencies{Config: cfg, Logger: quietLogger(), Accounts: AccountProviders{ZCode: manager}})
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/zcode/claim", strings.NewReader(`{"plan_id":"current-plan"}`)))
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("response = %d %s, want status %d containing %s", rec.Code, rec.Body.String(), tt.wantStatus, tt.wantBody)
			}
			if calls != 1 {
				t.Fatalf("upstream calls = %d, want one", calls)
			}
		})
	}
}

func TestZCodeClaimRefreshesBillingSnapshots(t *testing.T) {
	for _, tt := range []struct {
		name        string
		claimCode   string
		wantRefresh bool
	}{
		{"successful claim", "0", true},
		{"already claimed", "1003", true},
		{"risk blocked", "3012", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolatePrometheus(t)
			claimed := false
			balanceCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/zcode-plan/billing/claim":
					claimed = tt.wantRefresh
					_, _ = w.Write([]byte(`{"code":` + tt.claimCode + `}`))
				case "/api/v1/zcode-plan/billing/balance":
					balanceCalls++
					if claimed {
						_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[{"plan_id":"current-plan","status":"active"}],"balances":[{"plan_id":"current-plan","remaining_units":123}]}}`))
					} else {
						_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[],"balances":[]}}`))
					}
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
			}))
			defer upstream.Close()
			manager := zcodebackend.NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
			manager.CaptchaSolverURL = ""
			manager.Issuer = upstream.URL
			manager.HTTPClient = upstream.Client()
			if err := manager.Store.Save(&zcodebackend.Credentials{AccessToken: "test-jwt"}); err != nil {
				t.Fatal(err)
			}
			if err := manager.SetCaptchaVerifyParam("test-proof"); err != nil {
				t.Fatal(err)
			}
			s := NewWithDependencies(Dependencies{Config: &config.Config{}, Logger: quietLogger(), Accounts: AccountProviders{ZCode: manager}})
			get := func(path string) string {
				t.Helper()
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
				}
				return rec.Body.String()
			}
			for _, path := range []string{"/api/zcode/usage", "/api/zcode/quota"} {
				if strings.Contains(get(path), `"current-plan"`) {
					t.Fatal("unexpected plan before claim")
				}
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/zcode/claim", strings.NewReader(`{"plan_id":"current-plan"}`)))
			wantStatus := http.StatusConflict
			if tt.wantRefresh {
				wantStatus = http.StatusOK
			}
			if rec.Code != wantStatus {
				t.Fatalf("claim response = %d %s", rec.Code, rec.Body.String())
			}
			for _, path := range []string{"/api/zcode/usage", "/api/zcode/quota"} {
				body := get(path)
				if strings.Contains(body, `"plan_id":"current-plan"`) != tt.wantRefresh {
					t.Fatalf("GET %s body=%s, want active plan=%v", path, body, tt.wantRefresh)
				}
				if tt.wantRefresh && path == "/api/zcode/quota" && !strings.Contains(body, `"remaining_units":123`) {
					t.Fatalf("quota was not refreshed: %s", body)
				}
			}
			wantCalls := 2
			if tt.wantRefresh {
				wantCalls = 4
			}
			if balanceCalls != wantCalls {
				t.Fatalf("balance calls=%d, want %d", balanceCalls, wantCalls)
			}
		})
	}
}
