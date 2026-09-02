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
	s := NewWithAllAccountAuth(cfg, quietLogger(), nil, nil, nil, nil, nil, manager)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/zcode/offers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"plan_id":"weekend-free-1024"`) {
		t.Errorf("body does not list the offer: %s", rec.Body.String())
	}
}
