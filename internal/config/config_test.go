package config

import (
	"strings"
	"testing"

	_ "github.com/denysvitali/llm-proxy/internal/backend/all"
)

func TestValidateAcceptsRegisteredBackends(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: "127.0.0.1:8090"},
		Backends: []BackendConfig{
			{Type: "venice"},
			{Type: "nous", APIKeyEnv: "NOUS_KEY"},
		},
		Routes: map[string]ModelRoute{
			"stealth/ox-alpha": {Backend: "nous"},
		},
		DefaultRoute: ModelRoute{Backend: "venice"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsUnknownBackend(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Listen: "127.0.0.1:8090"},
		Backends: []BackendConfig{{Type: "not-a-backend"}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted unknown backend type")
	}
	if !strings.Contains(err.Error(), `unknown type "not-a-backend"`) {
		t.Fatalf("error %q should name the unknown type", err)
	}
	if !strings.Contains(err.Error(), "venice") {
		t.Fatalf("error %q should list registered backends", err)
	}
}

func TestValidateRejectsUnknownBackendInRoute(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Listen: "127.0.0.1:8090"},
		Backends: []BackendConfig{{Type: "venice"}},
		Routes: map[string]ModelRoute{
			"foo": {Backend: "nonexistent"},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted route with unknown backend")
	}
	if !strings.Contains(err.Error(), `unknown backend "nonexistent"`) {
		t.Fatalf("error %q should name the unknown backend", err)
	}

	// A valid route must pass.
	cfg.Routes = map[string]ModelRoute{
		"foo": {Backend: "venice"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected valid route: %v", err)
	}
}

func TestValidateRejectsGrokAPIKeyConfiguration(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Listen: "127.0.0.1:8090"},
		Backends: []BackendConfig{{Type: "grok", APIKeyEnv: "GROK_API_KEY"}},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "does not support API keys") {
		t.Fatalf("Validate() error = %v, want Grok API-key rejection", err)
	}
}
