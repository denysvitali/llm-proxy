package config

import (
	"strings"
	"testing"
)

func TestModelMetadataValidation(t *testing.T) {
	positive, zero, negative, large := 100, 0, -1, 101
	medium, high := "medium", "high"
	tests := []struct {
		name    string
		entries []ModelMetadata
		want    string
	}{
		{"unknown", []ModelMetadata{{Model: "Exact/Model"}}, ""},
		{"verified", []ModelMetadata{{Model: "Exact/Model", ContextWindow: &positive, MaxOutputTokens: &positive, ReasoningEfforts: []string{"medium"}, DefaultReasoningEffort: &medium}}, ""},
		{"empty", []ModelMetadata{{}}, "model ID"},
		{"padded", []ModelMetadata{{Model: " model"}}, "model ID"},
		{"duplicate", []ModelMetadata{{Model: "m"}, {Model: "m"}}, "duplicate model"},
		{"zero", []ModelMetadata{{Model: "m", ContextWindow: &zero}}, "positive"},
		{"negative", []ModelMetadata{{Model: "m", MaxOutputTokens: &negative}}, "positive"},
		{"exceeds", []ModelMetadata{{Model: "m", ContextWindow: &positive, MaxOutputTokens: &large}}, "exceeds"},
		{"effort", []ModelMetadata{{Model: "m", ReasoningEfforts: []string{"max"}}}, "unsupported reasoning"},
		{"duplicate effort", []ModelMetadata{{Model: "m", ReasoningEfforts: []string{"high", "high"}}}, "duplicate reasoning"},
		{"default", []ModelMetadata{{Model: "m", ReasoningEfforts: []string{"medium"}, DefaultReasoningEffort: &high}}, "must be in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModelMetadata(tt.entries)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadModelMetadata(t *testing.T) {
	isolateEnv(t)
	isolateHome(t)
	t.Setenv("LLM_PROXY_CONFIG", writeFile(t, "config.yaml", `backends:
  - type: venice
    model_metadata:
      - model: Vendor/ExactModel
        context_window: 64000
        max_output_tokens: 8192
        reasoning_efforts: [low, high]
        default_reasoning_effort: high
        supports_verbosity: true
        supports_parallel_tools: true
        supports_reasoning_summary: true
      - model: vendor/exactmodel
`))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	entries := cfg.Backends[0].ModelMetadata
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	m := entries[0]
	if m.Model != "Vendor/ExactModel" || m.ContextWindow == nil || *m.ContextWindow != 64000 || m.MaxOutputTokens == nil || *m.MaxOutputTokens != 8192 || m.DefaultReasoningEffort == nil || *m.DefaultReasoningEffort != "high" || len(m.ReasoningEfforts) != 2 || !m.SupportsVerbosity || !m.SupportsParallelTools || !m.SupportsReasoningSummary {
		t.Fatalf("metadata = %+v", m)
	}
	if entries[1].Model != "vendor/exactmodel" || entries[1].ContextWindow != nil || entries[1].DefaultReasoningEffort != nil {
		t.Fatalf("unknown metadata = %+v", entries[1])
	}
	cfg.Backends[0].ModelMetadata[0].ContextWindow = new(int)
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "backends[venice]") {
		t.Fatalf("Validate = %v", err)
	}
}
