package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	minimaxcodebackend "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestMiniMaxAccountEndpoints(t *testing.T) {
	isolatePrometheus(t)
	var claims, reads atomic.Int32
	var failAccount, failCheckin atomic.Bool
	days := make([]map[string]any, 7)
	for i := range days {
		status := 1
		if i == 0 {
			status = 2
		}
		days[i] = map[string]any{"day_no": i + 1, "points": 200, "bonus_points": 100, "status": status, "is_today": i == 0}
	}
	panel := map[string]any{"scene": 1, "days": days}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Header.Get("Authorization") != "Bearer account-secret" {
			t.Error("missing account authentication")
		}
		if failAccount.Load() && strings.HasPrefix(r.URL.Path, "/matrix/") || failCheckin.Load() && strings.Contains(r.URL.Path, "/signin/") {
			http.Error(w, "upstream-secret must not leak", http.StatusBadGateway)
			return
		}
		var body any
		switch r.URL.Path {
		case "/v1/api/user/info":
			body = map[string]any{"data": map[string]any{"userInfo": map[string]any{"realUserID": "test-user"}}}
		case "/matrix/api/v1/user/get_user_extra_info":
			body = map[string]any{"workspaces": []any{map[string]any{"workspace_type": 0, "workspace_id": 42, "has_token_plan": false, "opcredit_balance": "0"}}}
		case "/matrix/api/v1/commerce/get_membership_info":
			body = map[string]any{"has_token_plan": false, "opcredit_balance": "0"}
		case "/minimax-cloud/api/v1/signin/status":
			if r.Method != http.MethodGet {
				t.Error("status must be read-only")
			}
			body = map[string]any{"data": panel}
		case "/minimax-cloud/api/v1/signin/claim":
			claims.Add(1)
			if r.Method != http.MethodPost {
				t.Error("claim must use POST")
			}
			body = map[string]any{"data": map[string]any{"claim_id": "claim-1", "claim_result": 1, "day_no": 1, "points": 200, "expire_at_ms": 1800000000000, "panel": panel}}
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, body)
	}))
	defer upstream.Close()
	manager := minimaxcodebackend.NewManager(filepath.Join(t.TempDir(), "account.json"))
	manager.AccountBaseURL = upstream.URL
	manager.PlatformBaseURL = upstream.URL
	if err := manager.Store.Save(&minimaxcodebackend.Credentials{AccessToken: "account-secret"}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Backends: []config.BackendConfig{{Type: "minimax-code"}}}
	s := NewWithAllAccountAuth(cfg, quietLogger(), nil, []backend.Backend{minimaxcodebackend.New("", "")}, nil, nil, nil, nil, manager)
	handler := s.Handler()
	request := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if strings.Contains(rec.Body.String(), "account-secret") || strings.Contains(rec.Body.String(), "upstream-secret") {
			t.Fatal("account endpoint leaked upstream details")
		}
		return rec
	}
	if rec := request(http.MethodGet, "/api/overview"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"minimaxUsage":{"configured":true,"available":true}`) {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body.String())
	}
	if reads.Load() != 0 {
		t.Fatal("overview metadata must not fetch account services")
	}
	rec := request(http.MethodGet, "/api/minimax-code/usage")
	var view minimaxUsageView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || view.Account == nil || view.Checkin == nil || view.FetchedAt.IsZero() || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body.String())
	}
	if claims.Load() != 0 {
		t.Fatal("usage read must never claim credits")
	}
	crossSite := httptest.NewRequest(http.MethodPost, "/api/minimax-code/checkin", nil)
	crossSite.Header.Set("Origin", "https://unrelated.example")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, crossSite)
	if rec.Code != http.StatusForbidden || claims.Load() != 0 {
		t.Fatal("cross-origin check-in must be rejected")
	}
	rec = request(http.MethodPost, "/api/minimax-code/checkin")
	if rec.Code != http.StatusOK || claims.Load() != 1 || !strings.Contains(rec.Body.String(), `"points":200`) {
		t.Fatalf("claim: %d %s, posts=%d", rec.Code, rec.Body.String(), claims.Load())
	}
	failAccount.Store(true)
	rec = request(http.MethodGet, "/api/minimax-code/usage")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"accountError"`) || !strings.Contains(rec.Body.String(), `"checkin"`) {
		t.Fatalf("partial failure: %d %s", rec.Code, rec.Body.String())
	}
	failCheckin.Store(true)
	rec = request(http.MethodGet, "/api/minimax-code/usage")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("total failure: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMiniMaxAccountUnavailable(t *testing.T) {
	isolatePrometheus(t)
	disabled := false
	for _, cfg := range []*config.Config{
		{},
		{Backends: []config.BackendConfig{{Type: "minimax-code"}}},
		{Backends: []config.BackendConfig{{Type: "minimax-code", Enabled: &disabled}}},
	} {
		s := New(cfg, quietLogger(), nil, nil)
		for _, call := range []struct{ method, path string }{{http.MethodGet, "/api/minimax-code/usage"}, {http.MethodPost, "/api/minimax-code/checkin"}} {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(call.method, call.path, nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s: %d", call.path, rec.Code)
			}
		}
	}
}
