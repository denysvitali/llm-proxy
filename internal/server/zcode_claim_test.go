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
