package server

import (
	"encoding/json"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestNormalizeCodexModelSelector(t *testing.T) {
	tests := []struct {
		name, input, model, effort string
		wantErr                    bool
	}{
		{name: "xhigh", input: "codex/gpt-5.6-luna:xhigh", model: "codex/gpt-5.6-luna", effort: "xhigh"},
		{name: "bare", input: "gpt-5.6-luna", model: "gpt-5.6-luna"},
		{name: "nested non codex", input: "openrouter/org/model:xhigh", model: "openrouter/org/model:xhigh"},
		{name: "unsupported", input: "codex/gpt-5.6-luna:turbo", wantErr: true},
		{name: "missing effort", input: "codex/gpt-5.6-luna:", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, effort, err := normalizeCodexModelSelector(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || model != tt.model || effort != tt.effort {
				t.Fatalf("normalize = %q, %q, %v; want %q, %q", model, effort, err, tt.model, tt.effort)
			}
		})
	}
}

func TestApplyCodexReasoningEffort(t *testing.T) {
	payload, err := applyCodexReasoningEffort(route{backend: &fakeOABackend{name: "codex"}}, resolvedWire{native: true}, []byte(`{"model":"gpt-5.6-luna","reasoning":{"summary":"auto"}}`), "xhigh")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	reasoning := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "xhigh" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
}

func TestApplyCodexReasoningEffortSkipsNonResponses(t *testing.T) {
	payload := []byte(`{"model":"x"}`)
	got, err := applyCodexReasoningEffort(route{backend: &fakeOABackend{name: "codex"}}, resolvedWire{path: &translationPath{kind: backend.KindOpenAIChat}}, payload, "xhigh")
	if err != nil || string(got) != string(payload) {
		t.Fatalf("got %s, err %v", got, err)
	}
}

func TestResponsesCodexSelectorRoutesBareModelAndEffort(t *testing.T) {
	fb := &fakeOABackend{
		name:  "codex",
		kinds: map[backend.Kind]bool{backend.KindOpenAIResponses: true},
		body:  `{"id":"resp_1","status":"completed","model":"gpt-5.6-luna","output":[]}`,
	}
	s := newOATestServer(t, fb, nil)
	rec := postOpenAI(t, s, "/v1/responses", `{"model":"codex/gpt-5.6-luna:xhigh","input":"hello"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	req := fb.lastRequest()
	if req == nil || req.Model != "gpt-5.6-luna" {
		t.Fatalf("request = %+v, want bare model", req)
	}
	var sent map[string]any
	if err := json.Unmarshal(req.RawBody, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["model"] != "gpt-5.6-luna" {
		t.Errorf("forwarded model = %v", sent["model"])
	}
	reasoning, ok := sent["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "xhigh" {
		t.Errorf("forwarded reasoning = %#v", sent["reasoning"])
	}
}
