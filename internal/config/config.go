// Package config loads llm-proxy configuration from flags, environment
// variables (LLM_PROXY_ prefix), a YAML file, then defaults.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	// Fallbacks names alternate backends (with optional model rewrites) that
	// serve the request when this backend fails before anything has reached
	// the client. Tried in order after the primary route's own fallbacks.
	Fallbacks []FallbackRoute `mapstructure:"fallbacks"`
	// RetryAttempts caps the extra connection-phase attempts made after a
	// transient upstream failure while nothing has been forwarded. Zero means
	// the built-in default (10).
	RetryAttempts int `mapstructure:"retry_attempts"`
	// RetryMaxBackoff caps a single retry pause (exponential backoff from
	// 750ms, and provider Retry-After values). Zero means 30s.
	RetryMaxBackoff time.Duration `mapstructure:"retry_max_backoff"`
}

// FallbackRoute is one alternate backend for a failing route: the backend
// takes the request, with the model optionally rewritten for that backend.
type FallbackRoute struct {
	Backend string `mapstructure:"backend"`
	Model   string `mapstructure:"model"`
}

// ModelRoute maps an inbound model name to backend + upstream model.
type ModelRoute struct {
	Backend string `mapstructure:"backend"`
	Model   string `mapstructure:"model"`
	// Fallbacks are tried in order when the primary backend fails before
	// anything has reached the client.
	Fallbacks []FallbackRoute `mapstructure:"fallbacks"`
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

// StatsConfig controls usage-stats persistence and retention.
type StatsConfig struct {
	// PersistFile is the JSON snapshot path. Empty disables persistence.
	PersistFile string `mapstructure:"persist_file"`
	// PersistInterval is how often the in-memory stats are flushed to disk.
	PersistInterval time.Duration `mapstructure:"persist_interval"`
	// RetentionDays drops buckets older than this (0 = keep forever).
	RetentionDays int `mapstructure:"retention_days"`
}

// Config is the whole configuration document.
type Config struct {
	BaseURL string `mapstructure:"base_url"`
	// GrokAuthFile stores the xAI account session used by the Grok
	// subscription backend. It is not an API key.
	GrokAuthFile string          `mapstructure:"grok_auth_file"`
	Server       ServerConfig    `mapstructure:"server"`
	Auth         AuthConfig      `mapstructure:"auth"`
	Backends     []BackendConfig `mapstructure:"backends"`
	Stats        StatsConfig     `mapstructure:"stats"`

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
	if c.GrokAuthFile == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.GrokAuthFile = filepath.Join(home, ".config", "grok-proxy", "auth.json")
		}
	}
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
	if c.Stats.PersistFile != "" && c.Stats.PersistInterval == 0 {
		c.Stats.PersistInterval = 60 * time.Second
	}
}

// Validate reports misconfiguration that would only surface at request time
// otherwise.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Server.Listen) == "" {
		return fmt.Errorf("server.listen must not be empty")
	}
	seen := map[string]bool{}
	// Keep pre-account-session Grok configs loadable during migration. The
	// Grok backend ignores these legacy fields and uses its TokenSource.
	for _, b := range c.Backends {
		if !backend.Has(b.Type) {
			return fmt.Errorf("backends: unknown type %q (registered: %v)", b.Type, backend.Names())
		}
		if seen[b.Type] {
			return fmt.Errorf("backends: duplicate type %q", b.Type)
		}
		seen[b.Type] = true
	}
	for name, r := range c.Routes {
		if !backend.Has(r.Backend) {
			return fmt.Errorf("routes[%s]: unknown backend %q (registered: %v)", name, r.Backend, backend.Names())
		}
		if err := validateFallbacks(fmt.Sprintf("routes[%s]", name), r.Fallbacks, seen); err != nil {
			return err
		}
	}
	if err := validateFallbacks("default_route", c.DefaultRoute.Fallbacks, seen); err != nil {
		return err
	}
	if d := c.DefaultRoute.Backend; d != "" && !seen[d] {
		return fmt.Errorf("default_route: unknown backend %q", d)
	}
	for _, b := range c.Backends {
		_ = b.Enabled
		if err := validateFallbacks(fmt.Sprintf("backends[%s]", b.Type), b.Fallbacks, seen); err != nil {
			return err
		}
		if b.RetryAttempts < 0 {
			return fmt.Errorf("backends[%s]: retry_attempts must not be negative, got %d", b.Type, b.RetryAttempts)
		}
		if b.RetryMaxBackoff != 0 && b.RetryMaxBackoff < time.Second {
			return fmt.Errorf("backends[%s]: retry_max_backoff must be at least 1s, got %v", b.Type, b.RetryMaxBackoff)
		}
	}
	if c.Stats.PersistInterval != 0 && c.Stats.PersistInterval < 5*time.Second {
		return fmt.Errorf("stats.persist_interval must be at least 5s, got %v", c.Stats.PersistInterval)
	}
	return nil
}

// validateFallbacks checks that every fallback names a configured backend
// type. seen maps backend types present in this config; a fallback may point
// at a backend type that is valid but not configured only when the registry
// knows it, so unknown types are rejected here and missing ones at request
// time.
func validateFallbacks(where string, fallbacks []FallbackRoute, seen map[string]bool) error {
	for _, f := range fallbacks {
		if !backend.Has(f.Backend) {
			return fmt.Errorf("%s: fallbacks: unknown backend %q (registered: %v)", where, f.Backend, backend.Names())
		}
		if !seen[f.Backend] {
			return fmt.Errorf("%s: fallbacks: backend %q is not configured", where, f.Backend)
		}
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
