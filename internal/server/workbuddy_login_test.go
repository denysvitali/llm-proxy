package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	workbuddybackend "github.com/denysvitali/llm-proxy/internal/backend/workbuddy"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestWorkBuddyLoginPage(t *testing.T) {
	isolatePrometheus(t)
	cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:8090"}}
	manager := workbuddybackend.NewManager(filepath.Join(t.TempDir(), "workbuddy-auth.json"))
	s := NewWithAccountAuth(cfg, quietLogger(), nil, nil, nil, manager)
	req := httptest.NewRequest(http.MethodGet, "/login/workbuddy", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sign in with WorkBuddy") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
