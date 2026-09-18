package zcode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransformStartPlanRequestAddsGatewayIdentityAndCacheControl(t *testing.T) {
	original := []byte(`{"model":"glm-5.3-flash","system":[{"type":"text","text":"client system"}],"messages":[{"role":"user","content":"hello"}]}`)
	transformed := transformStartPlanRequest(original, zcodeIdentity{DeviceMid: "device-mid", SessionID: "session-1"})

	var body map[string]any
	if err := json.Unmarshal(transformed, &body); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	system, ok := body["system"].([]any)
	if !ok || len(system) != 3 {
		t.Fatalf("system blocks = %#v, want 3 blocks", body["system"])
	}
	encodedSystem, _ := json.Marshal(system)
	for _, want := range []string{"You are ZCode", "# Communicating with the user", "# Context management", "powered by the model named account:zai-start-plan/GLM-5.3-Flash"} {
		if !strings.Contains(string(encodedSystem), want) {
			t.Errorf("system blocks do not contain %q", want)
		}
	}
	if strings.Contains(string(encodedSystem), "client system") {
		t.Errorf("system blocks leak the inbound client prompt: %s", encodedSystem)
	}
	contextBlock := system[2].(map[string]any)
	if contextText, _ := contextBlock["text"].(string); !strings.Contains(contextText, "- You are powered by the model named account:zai-start-plan/GLM-5.3-Flash.\n\n# Context management") {
		t.Errorf("model line must occur at the end of the embedded Environment section, got %q", contextText)
	}
	if got := body["model"]; got != "GLM-5.3-Flash" {
		t.Errorf("model = %v, want canonical GLM-5.3-Flash", got)
	}
	if got := body["max_tokens"]; got != float64(128000) {
		t.Errorf("max_tokens = %v, want captured value 128000", got)
	}
	if thinking, _ := body["thinking"].(map[string]any); thinking["type"] != "enabled" {
		t.Errorf("thinking = %#v, want enabled", body["thinking"])
	}
	if output, _ := body["output_config"].(map[string]any); output["effort"] != "max" {
		t.Errorf("output_config = %#v, want max effort", body["output_config"])
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

func TestTransformStartPlanRequestReplacesArraySystemPrompt(t *testing.T) {
	original := []byte(`{"model":"glm-5.3-flash","system":[{"type":"text","text":"You are Claude Code"},{"type":"text","text":"client-only instructions"}],"messages":[{"role":"user","content":"hello"}]}`)
	transformed := transformStartPlanRequest(original, zcodeIdentity{DeviceMid: "device-mid", SessionID: "session-1"})

	if strings.Contains(string(transformed), "Claude Code") || strings.Contains(string(transformed), "client-only instructions") {
		t.Fatalf("transformed body leaks the inbound client system prompt: %s", transformed)
	}
	var body map[string]any
	if err := json.Unmarshal(transformed, &body); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	system, ok := body["system"].([]any)
	if !ok || len(system) != 3 {
		t.Fatalf("system blocks = %#v, want the captured 3-block ZCode envelope", body["system"])
	}
}

func TestTransformStartPlanRequestPreservesMalformedBody(t *testing.T) {
	original := []byte(`{"model":`)
	if got := transformStartPlanRequest(original, zcodeIdentity{DeviceMid: "device-mid", SessionID: "session-1"}); string(got) != string(original) {
		t.Errorf("malformed body changed to %q", got)
	}
}

func TestTransformStartPlanRequestReplacesClientMetadata(t *testing.T) {
	// Claude Code stamps metadata.user_id with user_<hash>_account_<uuid>_
	// session_<uuid>; the official client replaces it with a device identity
	// JSON string and drops every other metadata key. The transform must do
	// the same so client identifiers never reach the plan gateway.
	original := []byte(`{"model":"glm-5.3-flash","metadata":{"user_id":"user_5f3a_account_1e2d3c4b-5a6b-7c8d-9e0f-1a2b3c4d5e6f_session_0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","other":"keep-out"},"messages":[]}`)
	transformed := transformStartPlanRequest(original, zcodeIdentity{DeviceMid: "device-mid", SessionID: "session-1"})

	var body map[string]any
	if err := json.Unmarshal(transformed, &body); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v, want object", body["metadata"])
	}
	wantUserID := `{"device_id":"device-mid","account_uuid":"","session_id":"session-1"}`
	if got := metadata["user_id"]; got != wantUserID {
		t.Errorf("metadata.user_id = %v, want %s", got, wantUserID)
	}
	if len(metadata) != 1 {
		t.Errorf("metadata = %#v, want exactly the user_id key", metadata)
	}
	if strings.Contains(string(transformed), "_account_") || strings.Contains(string(transformed), "keep-out") {
		t.Errorf("transformed body leaks client identifiers: %s", transformed)
	}
}

func TestTransformStartPlanRequestAddsMetadataWhenClientSentNone(t *testing.T) {
	// The official client always emits metadata.user_id for Anthropic model
	// requests, so requests without inbound metadata gain the device identity.
	original := []byte(`{"model":"glm-5.3-flash","messages":[]}`)
	transformed := transformStartPlanRequest(original, zcodeIdentity{DeviceMid: "device-mid", SessionID: "session-1"})

	var body map[string]any
	if err := json.Unmarshal(transformed, &body); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok || metadata["user_id"] != `{"device_id":"device-mid","account_uuid":"","session_id":"session-1"}` {
		t.Fatalf("metadata = %#v, want synthesized device identity", body["metadata"])
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
		{name: "start plan legacy model", model: "glm-5.2", want: "GLM-5.2"},
		{name: "start plan turbo model", model: "glm-5-turbo", want: "GLM-5-Turbo"},
		{name: "unknown model passes through", model: "glm-4.7", want: "glm-4.7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transformed := transformStartPlanRequest([]byte(`{"model":"`+strings.TrimSpace(test.model)+`","messages":[]}`), zcodeIdentity{DeviceMid: "device-mid", SessionID: "session-1"})
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
	transformed := transformStartPlanRequest([]byte(`{"messages":[]}`), zcodeIdentity{DeviceMid: "device-mid", SessionID: "session-1"})
	var body map[string]any
	if err := json.Unmarshal(transformed, &body); err != nil {
		t.Fatalf("decode transformed request: %v", err)
	}
	encodedSystem, _ := json.Marshal(body["system"])
	if strings.Contains(string(encodedSystem), "powered by the model named") {
		t.Errorf("model line present without a model: %s", encodedSystem)
	}
}
