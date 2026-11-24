package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	zaibackend "github.com/denysvitali/llm-proxy/internal/backend/zai"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestZaiUsageEndpoint(t *testing.T) {
	fetches := 0
	s := newTestServer(t, nil)
	s.zaiUsage = &zaiUsageCache{fetch: func(context.Context) (zaibackend.Usage, error) {
		fetches++
		return zaibackend.Usage{Plan: "GLM Coding Pro", Level: "pro", FetchedAt: time.Now(),
			Windows: []zaibackend.Window{{Kind: "prompts", DurationSeconds: 18000, UsedPercent: 17}}}, nil
	}}
	var usage zaibackend.Usage
	for range 2 {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/zai/usage", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
			t.Fatal(err)
		}
	}
	if fetches != 1 {
		t.Errorf("fetches = %d, want 1 within the TTL", fetches)
	}
	if usage.Plan != "GLM Coding Pro" || len(usage.Windows) != 1 || usage.Windows[0].UsedPercent != 17 {
		t.Errorf("usage = %+v", usage)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if !contains(rec.Body.String(), `"zaiUsage":{"configured":true,"available":true}`) {
		t.Errorf("overview missing usage metadata: %s", rec.Body.String())
	}
}

func TestZaiUsageErrors(t *testing.T) {
	s := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/zai/usage", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status = %d", rec.Code)
	}
	for name, fetchErr := range map[string]error{
		"unauthorized": zaibackend.ErrUsageUnauthorized,
		"upstream":     errors.New("z.ai usage: connection refused"),
	} {
		t.Run(name, func(t *testing.T) {
			s.zaiUsage = &zaiUsageCache{fetch: func(context.Context) (zaibackend.Usage, error) {
				return zaibackend.Usage{}, fetchErr
			}}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/zai/usage", nil))
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestZaiUsageCacheConfig(t *testing.T) {
	t.Setenv("ZAI_TEST_KEY", "k")
	disabled := false
	for _, tc := range []struct {
		name string
		cfg  config.BackendConfig
		want bool
	}{
		{"configured", config.BackendConfig{Type: "zai", APIKeyEnv: "ZAI_TEST_KEY"}, true},
		{"no key", config.BackendConfig{Type: "zai", APIKeyEnv: "ZAI_MISSING_KEY"}, false},
		{"disabled", config.BackendConfig{Type: "zai", APIKeyEnv: "ZAI_TEST_KEY", Enabled: &disabled}, false},
		{"other backend", config.BackendConfig{Type: "venice", APIKeyEnv: "ZAI_TEST_KEY"}, false},
	} {
		if got := newZaiUsageCache([]config.BackendConfig{tc.cfg}) != nil; got != tc.want {
			t.Errorf("%s: configured = %v, want %v", tc.name, got, tc.want)
		}
	}
}
