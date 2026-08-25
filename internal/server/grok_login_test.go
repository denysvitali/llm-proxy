package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestGrokLoginPageIsWebOnly(t *testing.T) {
	isolatePrometheus(t)
	cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:8090"}}
	manager := grokbackend.NewManager(filepath.Join(t.TempDir(), "grok-auth.json"))
	s := NewWithGrokAuth(cfg, quietLogger(), nil, nil, manager)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Sign in to Grok", "Sign in with xAI", "device authorization"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("login page does not contain %q", want)
		}
	}
}
