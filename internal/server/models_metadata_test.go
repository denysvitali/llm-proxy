package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

type metadataBackend struct {
	fakeBackend
	kind backend.Kind
}

func (b *metadataBackend) SupportsModel(kind backend.Kind, model string) bool { return kind == b.kind }

func TestCodexUnknownCapabilities(t *testing.T) {
	m := newCodexModel("provider/unknown", "provider", config.ModelMetadata{})
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"context_window", "max_context_window", "max_output_tokens", "default_reasoning_level", "default_verbosity", "apply_patch_tool_type", "tool_mode"} {
		if string(raw[key]) != "null" {
			t.Errorf("%s = %s, want null", key, raw[key])
		}
	}
	for _, key := range []string{"reasoning", "support_verbosity", "supports_parallel_tool_calls", "supports_reasoning_summaries", "supports_reasoning_summary_parameter", "supports_search_tool"} {
		if string(raw[key]) != "false" {
			t.Errorf("%s = %s, want false", key, raw[key])
		}
	}
	if string(raw["supported_reasoning_levels"]) != "[]" {
		t.Errorf("efforts = %s", raw["supported_reasoning_levels"])
	}
	if m.TruncationPolicy.Mode != "bytes" || m.TruncationPolicy.Limit != 10000 {
		t.Fatalf("tool output policy = %+v", m.TruncationPolicy)
	}
}

func TestCodexOfflineCatalogParser(t *testing.T) {
	binary := os.Getenv("LLM_PROXY_TEST_CODEX")
	if binary == "" {
		t.Skip("set LLM_PROXY_TEST_CODEX to a local Codex binary for offline parser qualification")
	}
	window, output, effort := 64000, 8192, "high"
	catalog := codexModelsResponse{Models: []codexModel{
		newCodexModel("test/unknown", "test", config.ModelMetadata{}),
		newCodexModel("test/verified", "test", config.ModelMetadata{ContextWindow: &window, MaxOutputTokens: &output, ReasoningEfforts: []string{"low", "high"}, DefaultReasoningEffort: &effort}),
	}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	path := filepath.Join(home, "catalog.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "debug", "models", "-c", "model_catalog_json="+path)
	cmd.Dir = home
	// Do not inherit credentials, config, or provider endpoints from the host.
	// The authoritative local catalog selects Codex's static model manager.
	cmd.Env = []string{"HOME=" + home, "CODEX_HOME=" + home, "PATH=" + os.Getenv("PATH"), "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "ALL_PROXY=http://127.0.0.1:1"}
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("Codex catalog parser: %v: %s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	var decoded codexModelsResponse
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode parser output: %v: %s", err, out)
	}
	if len(decoded.Models) != 2 || decoded.Models[0].Slug != "test/unknown" || decoded.Models[0].ContextWindow != nil || decoded.Models[1].ContextWindow == nil || *decoded.Models[1].ContextWindow != window {
		t.Fatalf("parser changed catalog: %s", out)
	}
}

func TestCodexExactModelMetadata(t *testing.T) {
	window, output, effort := 64000, 8192, "high"
	metadata := config.ModelMetadata{Model: "Vendor/Exact", ContextWindow: &window, MaxOutputTokens: &output, ReasoningEfforts: []string{"low", "high"}, DefaultReasoningEffort: &effort, SupportsVerbosity: true, SupportsParallelTools: true, SupportsReasoningSummary: true}
	for _, kind := range []backend.Kind{backend.KindOpenAIResponses, backend.KindOpenAIChat, backend.KindAnthropic} {
		t.Run(string(kind), func(t *testing.T) {
			s := newTestServer(t, []backend.Backend{
				&metadataBackend{fakeBackend: fakeBackend{name: "venice", models: []string{"Vendor/Exact", "vendor/exact", "Vendor/Exact-20260101"}}, kind: kind},
				&fakeBackend{name: "opencode", models: []string{"Vendor/Exact"}},
			}, config.BackendConfig{Type: "venice", ModelMetadata: []config.ModelMetadata{metadata}}, config.BackendConfig{Type: "opencode"})
			for _, url := range []string{"/v1/models", "/v1/models?backend=venice"} {
				rec, list := getCodexModels(t, s, url)
				if rec.Code != 200 {
					t.Fatalf("%s", rec.Body.String())
				}
				found := false
				for _, m := range list.Models {
					if m.Slug != "venice/Vendor/Exact" {
						if m.ContextWindow != nil || m.Reasoning || m.SupportVerbosity || m.SupportsParallelToolCalls {
							t.Errorf("metadata leaked into %s", m.Slug)
						}
						continue
					}
					found = true
					if m.ContextWindow == nil || *m.ContextWindow != window || m.MaxOutputTokens == nil || *m.MaxOutputTokens != output || !m.SupportsParallelToolCalls {
						t.Fatalf("configured metadata lost: %+v", m)
					}
					native := kind == backend.KindOpenAIResponses
					if m.SupportVerbosity != native || m.SupportsReasoningSummaries != native || m.SupportsReasoningSummaryParameter != native {
						t.Errorf("native controls advertised incorrectly: %+v", m)
					}
					wantEffort := kind != backend.KindAnthropic
					if m.Reasoning != wantEffort || (m.DefaultReasoningLevel != nil) != wantEffort {
						t.Errorf("reasoning advertised incorrectly: %+v", m)
					}
				}
				if !found {
					t.Fatal("missing configured model")
				}
			}
		})
	}
}
