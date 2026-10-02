package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	codexbackend "github.com/denysvitali/llm-proxy/internal/backend/codex"
	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	minimaxcodebackend "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
	workbuddybackend "github.com/denysvitali/llm-proxy/internal/backend/workbuddy"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestAccountProvidersShareLiveSessions(t *testing.T) {
	dir := t.TempDir()
	accounts := AccountProviders{
		Grok:        grokbackend.NewManager(filepath.Join(dir, "grok.json")),
		WorkBuddy:   workbuddybackend.NewManager(filepath.Join(dir, "workbuddy.json")),
		Codex:       codexbackend.NewManager(filepath.Join(dir, "codex.json")),
		ZCode:       zcodebackend.NewManager(filepath.Join(dir, "zcode.json")),
		MiniMaxCode: minimaxcodebackend.NewManager(filepath.Join(dir, "minimax.json")),
	}
	accounts.Grok.LegacyPath, accounts.Grok.GrokPath = "", ""
	tests := []struct {
		name, label string
		save        func() error
	}{
		{"grok", "xAI account", func() error {
			return accounts.Grok.Store.Save(&grokbackend.Token{AccessToken: "grok-token"})
		}},
		{"workbuddy", "WorkBuddy account", func() error {
			return os.WriteFile(accounts.WorkBuddy.Path, []byte(`{"auth":{"accessToken":"workbuddy-token"},"account":{}}`), 0600)
		}},
		{"codex", "ChatGPT account", func() error {
			return accounts.Codex.Store.Save(&codexbackend.Credentials{AccessToken: "codex-token", AccountID: "test-account"})
		}},
		{"zcode", "ZCode account", func() error {
			return accounts.ZCode.Store.Save(&zcodebackend.Credentials{AccessToken: "zcode-token"})
		}},
		{"minimax-code", "MiniMax Code account", func() error {
			return accounts.MiniMaxCode.Store.Save(&minimaxcodebackend.Credentials{AccessToken: "minimax-code-token"})
		}},
	}

	// Disabled backends exercise account status without fetching remote catalogs
	// or usage. A legacy API key must never masquerade as an account session.
	disabled := false
	cfg := &config.Config{}
	for _, tc := range tests {
		cfg.Backends = append(cfg.Backends, config.BackendConfig{Type: tc.name, Enabled: &disabled, APIKey: "legacy-key"})
	}
	s := NewWithDependencies(Dependencies{Config: cfg, Logger: quietLogger(), Accounts: accounts})
	t.Cleanup(func() { _ = s.Close() })
	tokens := accounts.TokenSources()
	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readStatus := func() overviewBackend {
				t.Helper()
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
				var page overviewPage
				if rec.Code != http.StatusOK {
					t.Fatalf("overview status = %d: %s", rec.Code, rec.Body.String())
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				return page.Backends[index]
			}
			if status := readStatus(); status.AuthConfigured || status.HasKey || status.AuthLabel != tc.label {
				t.Fatalf("signed-out account status = %+v", status)
			}
			if err := tc.save(); err != nil {
				t.Fatal(err)
			}
			if status := readStatus(); !status.AuthConfigured || status.HasKey || status.AuthLabel != tc.label {
				t.Fatalf("signed-in account status = %+v", status)
			}
			source := tokens[tc.name]
			if source == nil {
				t.Fatal("missing token source")
			}
			if token, err := source.AccessToken(context.Background()); err != nil || token != tc.name+"-token" {
				t.Fatalf("backend token = %q, error = %v", token, err)
			}
		})
	}
}

func TestAccountRoutesWithoutManagers(t *testing.T) {
	if sources := (AccountProviders{}).TokenSources(); len(sources) != 0 {
		t.Fatalf("absent managers produced token sources: %v", sources)
	}
	s := NewWithDependencies(Dependencies{Logger: quietLogger()})
	t.Cleanup(func() { _ = s.Close() })
	handler := s.Handler()
	for _, path := range []string{"/login", "/login/workbuddy", "/login/codex", "/login/zcode", "/login/minimax-code"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(method+path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
	for _, path := range []string{"/api/grok/usage", "/api/zcode/usage", "/api/zcode/quota", "/api/zcode/balance", "/api/zcode/offers", "/api/minimax-code/usage"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}
