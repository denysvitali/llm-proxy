package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Load reads configuration for the CLI. Precedence: environment variables
// (LLM_PROXY_ prefix, dots become underscores) over the config file over
// defaults. LLM_PROXY_CONFIG points at an explicit file; otherwise
// ~/.config/llm-proxy/config.yaml is used when present. Callers apply
// command-line flags afterwards.
func Load() (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix("LLM_PROXY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Register defaults for the scalar keys: viper's AutomaticEnv only
	// consults the environment for keys it already knows about, so without
	// these an LLM_PROXY_* variable would be silently ignored whenever the
	// key was absent from the config file.
	v.SetDefault("server.listen", "127.0.0.1:8090")
	v.SetDefault("server.max_body_bytes", 16<<20)
	v.SetDefault("stats.redis_url", "")
	v.SetDefault("stats.redis_key_prefix", "llm-proxy:stats:")
	if home, err := os.UserHomeDir(); err == nil {
		v.SetDefault("stats.persist_file", filepath.Join(home, ".local", "state", "llm-proxy", "stats.json"))
	}
	// Keep Grok's credential file compatible with grok-proxy so an existing
	// account session can be reused without introducing an API-key setting.
	if home, err := os.UserHomeDir(); err == nil {
		v.SetDefault("grok_auth_file", filepath.Join(home, ".config", "grok-proxy", "auth.json"))
		v.SetDefault("codex_auth_file", filepath.Join(home, ".config", "llm-proxy", "codex-auth.json"))
		v.SetDefault("zcode_auth_file", filepath.Join(home, ".config", "llm-proxy", "zcode-auth.json"))
	}
	v.SetDefault("log_level", "info")
	v.SetDefault("log_format", "text")

	path := os.Getenv("LLM_PROXY_CONFIG")
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			candidate := filepath.Join(home, ".config", "llm-proxy", "config.yaml")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			}
		}
	}
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, err
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	cfg.Defaults()
	// RetentionDays: 0 means "keep forever"; apply the 7-day default only when
	// persistence is enabled and the field was not set explicitly in the config
	// file or environment.
	if cfg.Stats.PersistFile != "" && !v.IsSet("stats.retention_days") {
		cfg.Stats.RetentionDays = 7
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
