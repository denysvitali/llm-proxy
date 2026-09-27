package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	minimaxcodebackend "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestMiniMaxCodeLoginAndOverview(t *testing.T) {
	isolatePrometheus(t)
	manager := minimaxcodebackend.NewManager(filepath.Join(t.TempDir(), "minimax-code-auth.json"))
	cfg := &config.Config{Backends: []config.BackendConfig{{Type: "minimax-code"}}}
	s := NewWithAllAccountAuth(cfg, quietLogger(), nil, []backend.Backend{minimaxcodebackend.New("", "")}, nil, nil, nil, nil, manager)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login/minimax-code", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sign in with MiniMax") {
		t.Errorf("login page = %d, %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"authLabel":"MiniMax Code account"`) || !strings.Contains(rec.Body.String(), `"authConfigured":false`) {
		t.Errorf("overview = %d, %s", rec.Code, rec.Body.String())
	}
	if err := manager.Store.Save(&minimaxcodebackend.Credentials{AccessToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"authConfigured":true`) {
		t.Errorf("signed-in overview = %d, %s", rec.Code, rec.Body.String())
	}
}
