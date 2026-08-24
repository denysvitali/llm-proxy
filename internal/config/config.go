// Package config loads llm-proxy configuration from flags, environment
// variables (LLM_PROXY_ prefix), a YAML file, then defaults.
package config

import (
	"fmt"
	"strings"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// BackendConfig is one upstream provider entry.
type BackendConfig struct {
	// Type is the registered backend identifier ("venice", "opencode",
	// "grok", "nous", "apodex", ...). Valid types come from the backend registry;
	// binaries populate it by importing internal/backend/all.
	Type string `mapstructure:"type"`
	// BaseURL overrides the provider default endpoint.
	BaseURL string `mapstructure:"base_url"`
	// APIKeyEnv names an environment variable holding the upstream key; this
	// keeps secrets out of the config file itself.
	APIKeyEnv string `mapstructure:"api_key_env"`
	// APIKey is a literal upstream key. Only used when APIKeyEnv is unset.
	APIKey string `mapstructure:"api_key"`
	// Enabled defaults to true; set false to compile the backend out of routing.
	Enabled *bool `mapstructure:"enabled"`
	// DefaultModel is used when a client model cannot be routed to this
	// backend's catalog.
	DefaultModel string `mapstructure:"default_model"`
	// FreeOnly restricts the backend to zero-cost models. Only backends with
	// published pricing (venice) honor it; others ignore the flag.
	FreeOnly bool `mapstructure:"free_only"`
}

// ModelRoute maps an inbound model name to backend + upstream model.
type ModelRoute struct {
	Backend string `mapstructure:"backend"`
	Model   string `mapstructure:"model"`
}

// ServerConfig holds listener settings.
type ServerConfig struct {
	Listen       string `mapstructure:"listen"`
	MaxBodyBytes int64  `mapstructure:"max_body_bytes"`
}

// AuthConfig controls the user API-key store.
type AuthConfig struct {
	// File is the JSON key-store path. Empty disables authentication
	// (loopback-only deployments).
	File string `mapstructure:"file"`
}

// Config is the whole configuration document.
type Config struct {
	BaseURL  string          `mapstructure:"base_url"`
	Server   ServerConfig    `mapstructure:"server"`
	Auth     AuthConfig      `mapstructure:"auth"`
	Backends []BackendConfig `mapstructure:"backends"`

	// Routes maps inbound model name -> explicit route. Models not listed are
	// matched against each enabled backend's catalog (first match wins in
	// Backends order), then fall back to DefaultRoute.
	Routes map[string]ModelRoute `mapstructure:"routes"`
	// DefaultRoute is used when no route or catalog match exists.
	DefaultRoute ModelRoute `mapstructure:"default_route"`

	LogLevel  string `mapstructure:"log_level"`
	LogFormat string `mapstructure:"log_format"`
}

// Defaults fills zero-value fields with the documented defaults.
func (c *Config) Defaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = "127.0.0.1:8090"
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 16 << 20
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.LogFormat == "" {
		c.LogFormat = "text"
	}
	if c.Routes == nil {
		c.Routes = map[string]ModelRoute{}
	}
}

// Validate reports misconfiguration that would only surface at request time
// otherwise.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Server.Listen) == "" {
		return fmt.Errorf("server.listen must not be empty")
	}
	seen := map[string]bool{}
	for _, b := range c.Backends {
		if !backend.Has(b.Type) {
			return fmt.Errorf("backends: unknown type %q (registered: %v)", b.Type, backend.Names())
		}
		if seen[b.Type] {
			return fmt.Errorf("backends: duplicate type %q", b.Type)
		}
		seen[b.Type] = true
		for name, r := range c.Routes {
			if r.Backend == b.Type && !seen[r.Backend] {
				return fmt.Errorf("routes[%s]: unknown backend %q", name, r.Backend)
			}
		}
	}
	if d := c.DefaultRoute.Backend; d != "" && !seen[d] {
		return fmt.Errorf("default_route: unknown backend %q", d)
	}
	for _, b := range c.Backends {
		_ = b.Enabled
	}
	return nil
}

// BackendByType returns the backend entry of the given type.
func (c *Config) BackendByType(t string) (BackendConfig, bool) {
	for _, b := range c.Backends {
		if b.Type == t {
			return b, true
		}
	}
	return BackendConfig{}, false
}

// EnabledBackends returns entries whose Enabled flag is not false, in
// configuration order.
func (c *Config) EnabledBackends() []BackendConfig {
	out := make([]BackendConfig, 0, len(c.Backends))
	for _, b := range c.Backends {
		if b.IsEnabled() {
			out = append(out, b)
		}
	}
	return out
}

// ResolveKey returns the upstream key for a backend: env var first, literal second.
func (b BackendConfig) ResolveKey(lookup func(string) string) string {
	if b.APIKeyEnv != "" && lookup != nil {
		if v := lookup(b.APIKeyEnv); v != "" {
			return v
		}
	}
	return b.APIKey
}

// IsEnabled reports whether the backend participates in routing.
func (b BackendConfig) IsEnabled() bool {
	return b.Enabled == nil || *b.Enabled
}
