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

func TestZCodeLoginPageIsWebOnly(t *testing.T) {
	isolatePrometheus(t)
	cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:8090"}}
	manager := zcodebackend.NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	s := NewWithAllAccountAuth(cfg, quietLogger(), nil, nil, nil, nil, nil, manager)
	req := httptest.NewRequest(http.MethodGet, "/login/zcode", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Sign in to ZCode", "Sign in with ZCode", "No JWT or upstream API key"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("login page does not contain %q", want)
		}
	}
}
