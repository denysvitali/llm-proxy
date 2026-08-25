package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateEnv unsets every LLM_PROXY_* variable for the duration of the test
// and restores them afterwards, so the host environment cannot skew
// expectations. Tests using it must not run in parallel (process-wide env).
func isolateEnv(t *testing.T) {
	t.Helper()
	var restore []func()
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, "LLM_PROXY_") {
			continue
		}
		old, had := os.LookupEnv(key)
		_ = os.Unsetenv(key)
		if had {
			v := old
			restore = append(restore, func() { _ = os.Setenv(key, v) })
		}
	}
	t.Cleanup(func() {
		for _, f := range restore {
			f()
		}
	})
}

// isolateHome points HOME at an empty directory so Load never finds
// ~/.config/llm-proxy/config.yaml.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func boolPtr(b bool) *bool { return &b }

func TestLoadFromFileParsesSections(t *testing.T) {
	isolateEnv(t)
	isolateHome(t)

	yaml := `
base_url: https://proxy.example.com
server:
  listen: 10.0.0.1:7000
  max_body_bytes: 4096
auth:
  file: /var/lib/llm-proxy/keys.json
log_level: debug
log_format: json
backends:
  - type: venice
    base_url: https://venice.example.com/v1
    api_key_env: VENICE_API_KEY
    api_key: literal-secret
    enabled: false
    default_model: qwen-default
  - type: grok
    enabled: true
    default_model: grok-default
routes:
  gpt-4o:
    backend: venice
    model: qwen-large
default_route:
  backend: venice
  model: fallback-model
`
	path := writeFile(t, "config.yaml", yaml)
	t.Setenv("LLM_PROXY_CONFIG", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.BaseURL != "https://proxy.example.com" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Server.Listen != "10.0.0.1:7000" {
		t.Errorf("Server.Listen = %q", cfg.Server.Listen)
	}
	if cfg.Server.MaxBodyBytes != 4096 {
		t.Errorf("Server.MaxBodyBytes = %d", cfg.Server.MaxBodyBytes)
	}
	if cfg.Auth.File != "/var/lib/llm-proxy/keys.json" {
		t.Errorf("Auth.File = %q", cfg.Auth.File)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q", cfg.LogFormat)
	}

	if len(cfg.Backends) != 2 {
		t.Fatalf("len(Backends) = %d, want 2", len(cfg.Backends))
	}
	b0 := cfg.Backends[0]
	if b0.Type != "venice" || b0.BaseURL != "https://venice.example.com/v1" ||
		b0.APIKeyEnv != "VENICE_API_KEY" || b0.APIKey != "literal-secret" ||
		b0.DefaultModel != "qwen-default" {
		t.Errorf("Backends[0] = %+v", b0)
	}
	if b0.Enabled == nil || *b0.Enabled {
		t.Errorf("Backends[0].Enabled = %v, want false", b0.Enabled)
	}
	b1 := cfg.Backends[1]
	if b1.Type != "grok" || b1.DefaultModel != "grok-default" {
		t.Errorf("Backends[1] = %+v", b1)
	}
	if b1.Enabled == nil || !*b1.Enabled {
		t.Errorf("Backends[1].Enabled = %v, want true", b1.Enabled)
	}

	route, ok := cfg.Routes["gpt-4o"]
	if !ok {
		t.Fatalf("Routes missing key \"gpt-4o\" (have %v)", cfg.Routes)
	}
	if route.Backend != "venice" || route.Model != "qwen-large" {
		t.Errorf("Routes[\"gpt-4o\"] = %+v", route)
	}
	if cfg.DefaultRoute.Backend != "venice" || cfg.DefaultRoute.Model != "fallback-model" {
		t.Errorf("DefaultRoute = %+v", cfg.DefaultRoute)
	}
}

// TestLoadEnvOverridesFileKeyInFile covers the plain case: the key exists in
// the config file, so viper knows it and AutomaticEnv applies the override.
func TestLoadEnvOverridesFileKeyInFile(t *testing.T) {
	isolateEnv(t)
	isolateHome(t)

	path := writeFile(t, "config.yaml", "server:\n  listen: 127.0.0.1:1111\n")
	t.Setenv("LLM_PROXY_CONFIG", path)
	t.Setenv("LLM_PROXY_SERVER_LISTEN", "127.0.0.1:9999")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9999" {
		t.Errorf("Server.Listen = %q, want env override 127.0.0.1:9999", cfg.Server.Listen)
	}
}

// TestLoadEnvOnlyOverrideApplies covers keys entirely absent from the file.
// Viper only consults the environment for keys it already knows about; the
// SetDefault calls in loader.go make these known even without a config entry.
func TestLoadEnvOnlyOverrideApplies(t *testing.T) {
	isolateEnv(t)
	isolateHome(t)

	path := writeFile(t, "config.yaml", "log_level: warn\n")
	t.Setenv("LLM_PROXY_CONFIG", path)
	t.Setenv("LLM_PROXY_SERVER_LISTEN", "127.0.0.1:9999")
	t.Setenv("LLM_PROXY_SERVER_MAX_BODY_BYTES", "12345")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9999" {
		t.Errorf("Server.Listen = %q, want env-only override 127.0.0.1:9999", cfg.Server.Listen)
	}
	if cfg.Server.MaxBodyBytes != 12345 {
		t.Errorf("Server.MaxBodyBytes = %d, want env-only override 12345", cfg.Server.MaxBodyBytes)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn from file (env must not clobber it)", cfg.LogLevel)
	}
}

func TestLoadDefaultsWhenNoFileExists(t *testing.T) {
	isolateEnv(t)  // removes LLM_PROXY_CONFIG too
	isolateHome(t) // empty HOME: no ~/.config/llm-proxy/config.yaml

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8090" {
		t.Errorf("Server.Listen = %q, want 127.0.0.1:8090", cfg.Server.Listen)
	}
	if cfg.Server.MaxBodyBytes != 16777216 {
		t.Errorf("Server.MaxBodyBytes = %d, want 16777216", cfg.Server.MaxBodyBytes)
	}
	home := os.Getenv("HOME")
	wantStatsPath := filepath.Join(home, ".local", "state", "llm-proxy", "stats.json")
	if cfg.Stats.PersistFile != wantStatsPath {
		t.Errorf("Stats.PersistFile = %q, want %q", cfg.Stats.PersistFile, wantStatsPath)
	}
	if cfg.Stats.PersistInterval != time.Minute {
		t.Errorf("Stats.PersistInterval = %s, want one minute", cfg.Stats.PersistInterval)
	}
	if cfg.Stats.RetentionDays != 7 {
		t.Errorf("Stats.RetentionDays = %d, want 7", cfg.Stats.RetentionDays)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want text", cfg.LogFormat)
	}
	if cfg.Routes == nil {
		t.Error("Routes is nil, want non-nil empty map")
	} else if len(cfg.Routes) != 0 {
		t.Errorf("Routes = %v, want empty", cfg.Routes)
	}
}

func TestLoadStatsPersistFileEnvOverride(t *testing.T) {
	isolateEnv(t)
	isolateHome(t)
	path := writeFile(t, "stats.json", "{}")
	t.Setenv("LLM_PROXY_STATS_PERSIST_FILE", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Stats.PersistFile != path {
		t.Errorf("Stats.PersistFile = %q, want %q", cfg.Stats.PersistFile, path)
	}
	if cfg.Stats.PersistInterval != time.Minute {
		t.Errorf("Stats.PersistInterval = %s, want one minute", cfg.Stats.PersistInterval)
	}
	if cfg.Stats.RetentionDays != 7 {
		t.Errorf("Stats.RetentionDays = %d, want 7", cfg.Stats.RetentionDays)
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	isolateEnv(t)
	isolateHome(t)

	// Tab indentation is rejected by the YAML parser.
	path := writeFile(t, "broken.yaml", "backends:\n\t- type: venice\n")
	t.Setenv("LLM_PROXY_CONFIG", path)

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() with invalid YAML returned %+v, want error", cfg)
	}
}

func TestValidateRejections(t *testing.T) {
	listen := ServerConfig{Listen: "127.0.0.1:8090"}
	cases := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name:    "unknown backend type",
			cfg:     &Config{Server: listen, Backends: []BackendConfig{{Type: "anthropic"}}},
			wantErr: `unknown type "anthropic"`,
		},
		{
			name:    "duplicate backend type",
			cfg:     &Config{Server: listen, Backends: []BackendConfig{{Type: "venice"}, {Type: "venice"}}},
			wantErr: `duplicate type "venice"`,
		},
		{
			name: "default_route references missing backend",
			cfg: &Config{
				Server:       listen,
				Backends:     []BackendConfig{{Type: "venice"}},
				DefaultRoute: ModelRoute{Backend: "grok"},
			},
			wantErr: `default_route: unknown backend "grok"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %s", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() error = %q, want containing %s", err, tc.wantErr)
			}
		})
	}
}

func TestBackendByType(t *testing.T) {
	cfg := &Config{Backends: []BackendConfig{
		{Type: "venice", APIKey: "v-key"},
		{Type: "grok"},
	}}
	got, ok := cfg.BackendByType("venice")
	if !ok || got.APIKey != "v-key" {
		t.Errorf("BackendByType(\"venice\") = (%+v, %v)", got, ok)
	}
	got, ok = cfg.BackendByType("opencode")
	if ok || got.Type != "" {
		t.Errorf("BackendByType(\"opencode\") = (%+v, %v), want miss", got, ok)
	}
}

func TestEnabledBackendsRespectsFalseFlag(t *testing.T) {
	cfg := &Config{Backends: []BackendConfig{
		{Type: "venice"},
		{Type: "opencode", Enabled: boolPtr(true)},
		{Type: "grok", Enabled: boolPtr(false)},
	}}
	got := cfg.EnabledBackends()
	if len(got) != 2 {
		t.Fatalf("EnabledBackends() = %+v, want 2 entries", got)
	}
	if got[0].Type != "venice" || got[1].Type != "opencode" {
		t.Errorf("EnabledBackends() order = [%s %s], want [venice opencode]", got[0].Type, got[1].Type)
	}
}

func TestResolveKeyPrefersAPIKeyEnv(t *testing.T) {
	b := BackendConfig{APIKeyEnv: "TEST_UPSTREAM_KEY", APIKey: "literal"}
	fromEnv := func(name string) string {
		if name == "TEST_UPSTREAM_KEY" {
			return "from-env"
		}
		return ""
	}
	if got := b.ResolveKey(fromEnv); got != "from-env" {
		t.Errorf("ResolveKey(env set) = %q, want from-env", got)
	}
	// Empty env value falls back to the literal key.
	if got := b.ResolveKey(func(string) string { return "" }); got != "literal" {
		t.Errorf("ResolveKey(empty env) = %q, want literal", got)
	}
	// Nil lookup falls back to the literal key.
	if got := b.ResolveKey(nil); got != "literal" {
		t.Errorf("ResolveKey(nil lookup) = %q, want literal", got)
	}
	// No APIKeyEnv configured: lookup is not consulted.
	onlyLiteral := BackendConfig{APIKey: "only-literal"}
	if got := onlyLiteral.ResolveKey(func(string) string { return "ignored" }); got != "only-literal" {
		t.Errorf("ResolveKey(no APIKeyEnv) = %q, want only-literal", got)
	}
}

func TestIsEnabledNilMeansTrue(t *testing.T) {
	zero := BackendConfig{}
	if !zero.IsEnabled() {
		t.Error("nil Enabled should mean enabled")
	}
	on := BackendConfig{Enabled: boolPtr(true)}
	if !on.IsEnabled() {
		t.Error("Enabled=true should be enabled")
	}
	off := BackendConfig{Enabled: boolPtr(false)}
	if off.IsEnabled() {
		t.Error("Enabled=false should be disabled")
	}
}
