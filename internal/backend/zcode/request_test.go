package zcode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransformStartPlanRequestAddsGatewayIdentityAndCacheControl(t *testing.T) {
	original := []byte(`{"model":"glm-5.3-flash","system":[{"type":"text","text":"client system"}],"messages":[{"role":"user","content":"hello"}]}`)
	transformed := transformStartPlanRequest(original)

	var body map[string]any
	if err := json.Unmarshal(transformed, &body); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	system, ok := body["system"].([]any)
	if !ok || len(system) != 5 {
		t.Fatalf("system blocks = %#v, want 5 blocks", body["system"])
	}
	encodedSystem, _ := json.Marshal(system)
	for _, want := range []string{"You are ZCode", "# ZCode Desktop Context", "::code-comment", "powered by the model named GLM-5.3-Flash", "client system"} {
		if !strings.Contains(string(encodedSystem), want) {
			t.Errorf("system blocks do not contain %q", want)
		}
	}
	desktopBlock := system[2].(map[string]any)
	if desktopText, _ := desktopBlock["text"].(string); desktopText != zcodeDesktopContext {
		t.Errorf("desktop context block = %q, want verbatim official text", desktopText)
	}
	envBlock := system[3].(map[string]any)
	if envText, _ := envBlock["text"].(string); !strings.HasSuffix(envText, "- You are powered by the model named GLM-5.3-Flash.") {
		t.Errorf("model line must be the last line of the Environment block, got %q", envText)
	}
	if got := body["model"]; got != "GLM-5.3-Flash" {
		t.Errorf("model = %v, want canonical GLM-5.3-Flash", got)
	}
	messages := body["messages"].([]any)
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	block := content[0].(map[string]any)
	cache := block["cache_control"].(map[string]any)
	if cache["type"] != "ephemeral" {
		t.Errorf("cache_control = %#v, want ephemeral", cache)
	}
}

func TestTransformStartPlanRequestPreservesMalformedBody(t *testing.T) {
	original := []byte(`{"model":`)
	if got := transformStartPlanRequest(original); string(got) != string(original) {
		t.Errorf("malformed body changed to %q", got)
	}
}

func TestTransformStartPlanRequestCanonicalizesAndPassesThroughModels(t *testing.T) {
	for _, test := range []struct {
		name  string
		model string
		want  string
	}{
		{name: "known lowercase", model: "glm-5.3-flash", want: "GLM-5.3-Flash"},
		{name: "known with whitespace", model: " glm-5.3-flash ", want: "GLM-5.3-Flash"},
		{name: "already canonical", model: "GLM-5.3-Flash", want: "GLM-5.3-Flash"},
		{name: "unknown model passes through", model: "glm-4.7", want: "glm-4.7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transformed := transformStartPlanRequest([]byte(`{"model":"` + strings.TrimSpace(test.model) + `","messages":[]}`))
			var body map[string]any
			if err := json.Unmarshal(transformed, &body); err != nil {
				t.Fatalf("decode transformed request: %v", err)
			}
			if got := body["model"]; got != test.want {
				t.Errorf("model = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTransformStartPlanRequestOmitsModelLineWithoutModel(t *testing.T) {
	transformed := transformStartPlanRequest([]byte(`{"messages":[]}`))
	var body map[string]any
	if err := json.Unmarshal(transformed, &body); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	encodedSystem, _ := json.Marshal(body["system"])
	if strings.Contains(string(encodedSystem), "powered by the model named") {
		t.Errorf("model line present without a model: %s", encodedSystem)
	}
}
