package config

import (
	"testing"
	"time"
)

func TestLoadDocumentedScalarEnvironment(t *testing.T) {
	for _, withFile := range []bool{false, true} {
		name := "environment only"
		if withFile {
			name = "environment overrides file"
		}
		t.Run(name, func(t *testing.T) {
			isolateEnv(t)
			isolateHome(t)
			if withFile {
				t.Setenv("LLM_PROXY_CONFIG", writeFile(t, "config.yaml", `base_url: https://file.example
workbuddy_auth_file: /file/workbuddy.json
auth:
  file: /file/keys.json
stats:
  persist_interval: 1m
  retention_days: 7
`))
			}
			t.Setenv("LLM_PROXY_BASE_URL", "https://env.example")
			t.Setenv("LLM_PROXY_AUTH_FILE", "/env/keys.json")
			t.Setenv("LLM_PROXY_WORKBUDDY_AUTH_FILE", "/env/workbuddy.json")
			t.Setenv("LLM_PROXY_STATS_PERSIST_INTERVAL", "15s")
			t.Setenv("LLM_PROXY_STATS_RETENTION_DAYS", "0")
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.BaseURL != "https://env.example" {
				t.Errorf("base URL = %q", cfg.BaseURL)
			}
			if cfg.Auth.File != "/env/keys.json" {
				t.Errorf("auth file = %q", cfg.Auth.File)
			}
			if cfg.WorkBuddyAuthFile != "/env/workbuddy.json" {
				t.Errorf("WorkBuddy file = %q", cfg.WorkBuddyAuthFile)
			}
			if cfg.Stats.PersistInterval != 15*time.Second {
				t.Errorf("persist interval = %v", cfg.Stats.PersistInterval)
			}
			if cfg.Stats.RetentionDays != 0 {
				t.Errorf("retention = %d; explicit zero must survive", cfg.Stats.RetentionDays)
			}
		})
	}
}

func TestLoadEmptyEnvironmentFallsBackToFile(t *testing.T) {
	isolateEnv(t)
	isolateHome(t)
	t.Setenv("LLM_PROXY_CONFIG", writeFile(t, "config.yaml", `auth:
  file: /file/keys.json
stats:
  persist_file: ""
  retention_days: 0
`))
	t.Setenv("LLM_PROXY_AUTH_FILE", "")
	t.Setenv("LLM_PROXY_STATS_RETENTION_DAYS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.File != "/file/keys.json" {
		t.Errorf("empty env disabled configured authentication: %q", cfg.Auth.File)
	}
	if cfg.Stats.PersistFile != "" || cfg.Stats.RetentionDays != 0 {
		t.Errorf("explicit YAML empty/zero lost: %+v", cfg.Stats)
	}
}
