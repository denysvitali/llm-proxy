package translate

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNormalizeResponsesRequestForStrictUpstream(t *testing.T) {
	input := []byte(`{
		"model":"upstream-model",
		"instructions":"base rules",
		"input":[
			{"type":"message","role":"developer","content":[
				{"type":"input_text","text":"skills"},
				{"type":"input_text","text":"permissions"}
			]},
			{"type":"reasoning","encrypted_content":"opaque+/=","content":null},
			{"type":"message","role":"system","content":"stay terse"},
			{"type":"message","role":"user","content":"hello","custom":"keep"}
		],
		"custom_top_level":{"keep":true}
	}`)

	got, err := NormalizeResponsesRequest(input)
	if err != nil {
		t.Fatalf("NormalizeResponsesRequest: %v", err)
	}
	var request struct {
		Instructions string                       `json:"instructions"`
		Input        []map[string]json.RawMessage `json:"input"`
		Custom       map[string]bool              `json:"custom_top_level"`
	}
	if err := json.Unmarshal(got, &request); err != nil {
		t.Fatalf("decode normalized request: %v", err)
	}
	if want := "base rules\n\nskills\n\npermissions\n\nstay terse"; request.Instructions != want {
		t.Errorf("instructions = %q, want %q", request.Instructions, want)
	}
	if len(request.Input) != 2 {
		t.Fatalf("input items = %d, want reasoning + user", len(request.Input))
	}
	if _, exists := request.Input[0]["content"]; exists {
		t.Errorf("null reasoning content was not removed: %s", request.Input[0]["content"])
	}
	if string(request.Input[0]["encrypted_content"]) != `"opaque+/="` {
		t.Errorf("encrypted reasoning changed: %s", request.Input[0]["encrypted_content"])
	}
	if string(request.Input[1]["custom"]) != `"keep"` || !request.Custom["keep"] {
		t.Errorf("unknown fields were lost: input=%v top-level=%v", request.Input[1], request.Custom)
	}
	for _, item := range request.Input {
		var role string
		_ = json.Unmarshal(item["role"], &role)
		if isPromptRole(role) {
			t.Errorf("prompt role %q remains in input", role)
		}
	}
}

func TestNormalizeResponsesRequestLeavesCompatibleBodyUnchanged(t *testing.T) {
	input := []byte(`{ "model": "m", "instructions": "rules", "input": "hello" }`)
	got, err := NormalizeResponsesRequest(input)
	if err != nil {
		t.Fatalf("NormalizeResponsesRequest: %v", err)
	}
	if !bytes.Equal(got, input) {
		t.Errorf("compatible request changed: %q", got)
	}
}

func TestNormalizeResponsesRequestKeepsStructuredInstructions(t *testing.T) {
	input := []byte(`{
		"instructions":[{"type":"message","role":"developer","content":"rules"}],
		"input":[{"type":"reasoning","content":null,"encrypted_content":"opaque"}]
	}`)
	got, err := NormalizeResponsesRequest(input)
	if err != nil {
		t.Fatalf("NormalizeResponsesRequest: %v", err)
	}
	if !bytes.Contains(got, []byte(`"instructions":[`)) {
		t.Fatalf("structured instructions changed: %s", got)
	}
	if bytes.Contains(got, []byte(`"content":null`)) {
		t.Fatalf("null reasoning content remains: %s", got)
	}
}
