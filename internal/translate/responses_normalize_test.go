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
	if request.Instructions != "base rules" {
		t.Errorf("instructions changed: %q", request.Instructions)
	}
	if len(request.Input) != 4 {
		t.Fatalf("input items = %d, want all four in original order", len(request.Input))
	}
	if _, exists := request.Input[1]["content"]; exists {
		t.Errorf("null reasoning content was not removed: %s", request.Input[1]["content"])
	}
	if string(request.Input[1]["encrypted_content"]) != `"opaque+/="` {
		t.Errorf("encrypted reasoning changed: %s", request.Input[1]["encrypted_content"])
	}
	if string(request.Input[3]["custom"]) != `"keep"` || !request.Custom["keep"] {
		t.Errorf("unknown fields were lost: input=%v top-level=%v", request.Input[3], request.Custom)
	}
	if string(request.Input[0]["role"]) != `"developer"` || string(request.Input[2]["role"]) != `"system"` {
		t.Errorf("instruction roles/positions changed: %s", got)
	}
	if string(request.Input[2]["content"]) != `"stay terse"` {
		t.Errorf("system content changed: %s", got)
	}
	var content []map[string]string
	if err := json.Unmarshal(request.Input[0]["content"], &content); err != nil {
		t.Fatal(err)
	}
	if len(content) != 2 || content[0]["text"] != "skills" || content[1]["text"] != "permissions" {
		t.Errorf("developer content changed: %s", got)
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

func TestNormalizeResponsesRequestPreservesNativeHistory(t *testing.T) {
	for _, input := range []string{
		`{ "input": [{"role":"developer","content":"  rules\n"},{"role":"user","content":""},{"type":"message","role":"system","content":[{"type":"input_text","text":""}]}] }`,
		`{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]},{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"file contents"}]}]}`,
		`{"input":[{"type":"function_call_output","call_id":"call_1"},{"type":"custom_tool_call_output","call_id":"call_2","output":null}]}`,
	} {
		got, err := NormalizeResponsesRequest([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != input {
			t.Errorf("native history rewritten:\ngot  %s\nwant %s", got, input)
		}
	}
}
