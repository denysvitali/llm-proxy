package server

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// renderDashboard builds a Server, serves GET / through the full handler
// stack, and returns the recorder plus the unescaped page text.
func renderDashboard(t *testing.T, s *Server, header func(*http.Request)) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	if header != nil {
		header(req)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec, html.UnescapeString(rec.Body.String())
}

func TestHandleDashboard(t *testing.T) {
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

	rec, page := renderDashboard(t, s, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}

	for _, want := range []string{
		"llm-proxy",
		"127.0.0.1:8090",
		"authentication disabled",
		"venice",
		"grok",
		"api.venice.ai",
		"venice-large", // grouped catalog entries
		"venice-small",
		"catalog unavailable",                   // grok catalog failed
		"claude-sonnet-4",                       // routing table
		"anything unmatched",                    // default route row
		"ANTHROPIC_BASE_URL=http://example.com", // request Host used in snippet
		"claude --model venice-large",
		`wire_api = "responses"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("dashboard page missing %q", want)
		}
	}

	for _, leaked := range []string{secretKey, "sk-test"} {
		if strings.Contains(rec.Body.String(), leaked) || strings.Contains(page, leaked) {
			t.Errorf("dashboard leaks key material %q", leaked)
		}
	}
}

func TestHandleDashboardAuthEnabled(t *testing.T) {
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

	rec, page := renderDashboard(t, s, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+plainKey)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(page, "authentication enabled") {
		t.Error("dashboard does not report authentication enabled")
	}
	if strings.Contains(rec.Body.String(), plainKey) {
		t.Error("dashboard leaks the client API key")
	}

	unauthorized, _ := renderDashboard(t, s, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", unauthorized.Code)
	}
}
