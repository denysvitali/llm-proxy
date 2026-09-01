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
	for _, want := range []string{"You are ZCode", "powered by the model named glm-5.3-flash", "client system"} {
		if !strings.Contains(string(encodedSystem), want) {
			t.Errorf("system blocks do not contain %q", want)
		}
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
