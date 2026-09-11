package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestZcodeUsageEndpoint(t *testing.T) {
	isolatePrometheus(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/zcode-plan/billing/current" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[{"plan_id":"start-plan","status":"active","total_units":3000000,"used_units":1200000,"available_units":1800000}]}}`))
	}))
	t.Cleanup(upstream.Close)

	manager := zcodebackend.NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&zcodebackend.Credentials{AccessToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	s := NewWithAllAccountAuth(&config.Config{
		Backends: []config.BackendConfig{{Type: "zcode"}},
	}, quietLogger(), nil, nil, nil, nil, nil, manager)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/zcode/usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"plan_id":"start-plan"`, `"used_units":1200000`, `"available_units":1800000`, `"fetchedAt":`} {
		if !contains(rec.Body.String(), want) {
			t.Fatalf("body missing %s: %s", want, rec.Body.String())
		}
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestZcodeUsageEndpointUnavailable(t *testing.T) {
	isolatePrometheus(t)
	s := New(&config.Config{}, quietLogger(), nil, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/zcode/usage", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
