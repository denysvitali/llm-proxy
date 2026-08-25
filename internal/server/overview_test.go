package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// renderOverview builds a Server, serves GET /api/overview through the full
// handler stack, and returns the recorder plus decoded JSON.
func renderOverview(t *testing.T, s *Server, header func(*http.Request)) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	if header != nil {
		header(req)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var page map[string]any
	if rec.Code == http.StatusOK {
		page = decodeJSONMap(t, rec.Body.String())
	}
	return rec, page
}

func TestHandleOverview(t *testing.T) {
	isolatePrometheus(t)
	const secretKey = "sk-test-secret-do-not-leak"
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: "127.0.0.1:8090"},
		Backends: []config.BackendConfig{
			{Type: "venice", APIKey: secretKey, BaseURL: "https://api.venice.ai/api/v1"},
			{Type: "grok", APIKeyEnv: "GROK_KEY"},
		},
		Routes: map[string]config.ModelRoute{
			"claude-sonnet-4": {Backend: "venice"},
		},
		DefaultRoute: config.ModelRoute{Backend: "grok", Model: "grok-code"},
	}
	s := New(cfg, quietLogger(), nil, []backend.Backend{
		&fakeBackend{name: "venice", models: []string{"venice-large", "venice-small"}},
		&fakeBackend{name: "grok", err: errors.New("upstream down")},
	})

	rec, page := renderOverview(t, s, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertOverview(t, page, s, "example.com")

	for _, leaked := range []string{secretKey, "sk-test"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Errorf("dashboard leaks key material %q", leaked)
		}
	}
}

func TestHandleOverviewAuthEnabled(t *testing.T) {
	isolatePrometheus(t)
	store, err := auth.NewStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.CreateUser("alice"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	plainKey, err := store.CreateKey("alice", "test")
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	cfg := &config.Config{
		Backends: []config.BackendConfig{{Type: "venice", APIKey: plainKey}},
	}
	s := New(cfg, quietLogger(), store, []backend.Backend{
		&fakeBackend{name: "venice", models: []string{"venice-only"}},
	})

	rec, _ := renderOverview(t, s, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+plainKey)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), plainKey) {
		t.Error("dashboard leaks the client API key")
	}

	unauthorized, _ := renderOverview(t, s, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", unauthorized.Code)
	}
}

// assertOverview checks the API fields consumed by the dashboard SPA.
func assertOverview(t *testing.T, page map[string]any, s *Server, requestHost string) {
	t.Helper()

	if page["name"] != proxyName {
		t.Errorf("name = %v, want %q", page["name"], proxyName)
	}
	if page["listen"] != "127.0.0.1:8090" {
		t.Errorf("listen = %v, want 127.0.0.1:8090", page["listen"])
	}
	if page["authEnabled"] != false {
		t.Errorf("authEnabled = %v, want false", page["authEnabled"])
	}

	backends, ok := page["backends"].([]any)
	if !ok || len(backends) != 2 {
		t.Fatalf("backends = %#v, want two entries", page["backends"])
	}
	first, ok := backends[0].(map[string]any)
	if !ok {
		t.Fatalf("first backend = %#v, want object", backends[0])
	}
	if first["name"] != "venice" || first["host"] != "api.venice.ai" || first["hasKey"] != true {
		t.Errorf("venice backend = %#v", first)
	}
	models, ok := first["models"].([]any)
	if !ok || len(models) != 2 || models[0] != "venice-large" || models[1] != "venice-small" {
		t.Errorf("venice models = %#v, want sorted catalog entries", first["models"])
	}

	second, ok := backends[1].(map[string]any)
	if !ok || second["catalogOK"] != false {
		t.Errorf("grok backend = %#v, want catalog unavailable", backends[1])
	}

	routes, ok := page["routes"].([]any)
	if !ok || len(routes) != 1 {
		t.Fatalf("routes = %#v, want one entry", page["routes"])
	}
	route, ok := routes[0].(map[string]any)
	if !ok || route["model"] != "claude-sonnet-4" || route["backend"] != "venice" {
		t.Errorf("route = %#v", routes[0])
	}
	if page["hasDefault"] != true {
		t.Errorf("hasDefault = %v, want true", page["hasDefault"])
	}

	claudeSnippet, ok := page["claudeSnippet"].(string)
	if !ok || !strings.Contains(claudeSnippet,
		"ANTHROPIC_BASE_URL=http://"+requestHost+" ANTHROPIC_AUTH_TOKEN=<key> claude --model venice-large") {
		t.Errorf("claudeSnippet = %v", page["claudeSnippet"])
	}
	codexSnippet, ok := page["codexSnippet"].(string)
	if !ok || !strings.Contains(codexSnippet, `wire_api = "responses"`) {
		t.Errorf("codexSnippet = %v", page["codexSnippet"])
	}
}

func decodeJSONMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, body)
	}
	return decoded
}
