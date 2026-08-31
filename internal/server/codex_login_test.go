package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	codexbackend "github.com/denysvitali/llm-proxy/internal/backend/codex"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestCodexLoginPageIsWebOnly(t *testing.T) {
	isolatePrometheus(t)
	cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:8090"}}
	manager := codexbackend.NewManager(filepath.Join(t.TempDir(), "codex-auth.json"))
	s := NewWithAllAccountAuth(cfg, quietLogger(), nil, nil, nil, nil, manager)
	req := httptest.NewRequest(http.MethodGet, "/login/codex", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Sign in to Codex", "Sign in with ChatGPT", "one-time device code"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("login page does not contain %q", want)
		}
	}
}
