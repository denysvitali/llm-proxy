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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
