package translate

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBufferedResponsesEnvelope(t *testing.T) {
	request := []byte(`{"model":"client","input":"hello","instructions":"  be precise\n","metadata":{"label":""},"parallel_tool_calls":false,"temperature":0,"top_p":0,"max_output_tokens":12,"tool_choice":"none","tools":[],"reasoning":{"effort":"low"},"text":{"format":{"type":"text"}},"service_tier":"priority","store":false,"user":"","safety_identifier":"safe","prompt_cache_key":"cache","prompt_cache_retention":"24h","object":"not-response","created_at":1}`)
	for name, tc := range map[string]struct {
		convert func([]byte, string, []byte) ([]byte, error)
		body    string
	}{
		"chat":     {ResponsesFromChatForRequest, `{"id":"chat_1","created":123,"model":"upstream","choices":[{"finish_reason":"stop","message":{"content":""}}]}`},
		"messages": {ResponsesFromAnthropicForRequest, `{"id":"msg_1","model":"upstream","stop_reason":"end_turn","content":[{"type":"text","text":""}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := tc.convert([]byte(tc.body), "client", request)
			if err != nil {
				t.Fatal(err)
			}
			var fields, input map[string]json.RawMessage
			if err := json.Unmarshal(out, &fields); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(request, &input); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"id", "created_at", "object", "model", "status", "output", "parallel_tool_calls", "tool_choice", "tools", "usage", "error", "incomplete_details"} {
				if _, ok := fields[key]; !ok {
					t.Errorf("missing required envelope field %s: %s", key, out)
				}
			}
			for _, key := range []string{"instructions", "metadata", "temperature", "top_p", "max_output_tokens", "parallel_tool_calls", "tool_choice", "tools", "reasoning", "text", "store", "user", "safety_identifier", "prompt_cache_key", "prompt_cache_retention"} {
				if !bytes.Equal(fields[key], input[key]) {
					t.Errorf("%s = %s, want %s", key, fields[key], input[key])
				}
			}
			if string(fields["object"]) != `"response"` || string(fields["model"]) != `"upstream"` || string(fields["error"]) != "null" || string(fields["incomplete_details"]) != "null" {
				t.Fatalf("envelope facts = %s", out)
			}
			if _, ok := fields["service_tier"]; ok {
				t.Fatal("requested service tier presented as delivered tier")
			}
			var created int64
			if err := json.Unmarshal(fields["created_at"], &created); err != nil || created <= 1 || name == "chat" && created != 123 {
				t.Fatalf("created_at = %s", fields["created_at"])
			}
		})
	}
}

func TestGeneratedResponsesDefaultsAndIdentity(t *testing.T) {
	for name, tc := range map[string]struct {
		convert func([]byte, string) ([]byte, error)
		body    string
	}{
		"chat":     {ResponsesFromChat, `{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`},
		"messages": {ResponsesFromAnthropic, `{"stop_reason":"end_turn","content":[{"type":"text","text":""}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			ids := map[string]bool{}
			for range 2 {
				out, err := tc.convert([]byte(tc.body), "fallback")
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(out, &fields); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]string{"object": `"response"`, "parallel_tool_calls": "true", "tool_choice": `"auto"`, "tools": "[]", "model": `"fallback"`, "instructions": "null", "metadata": "null", "temperature": "null", "top_p": "null", "max_output_tokens": "null"} {
					if string(fields[key]) != want {
						t.Errorf("%s = %s, want %s", key, fields[key], want)
					}
				}
				var id string
				if err := json.Unmarshal(fields["id"], &id); err != nil || !strings.HasPrefix(id, "resp_") || ids[id] {
					t.Fatalf("missing/colliding generated ID %s", fields["id"])
				}
				ids[id] = true
			}
		})
	}
}

func TestResponsesEnvelopeNullDefaults(t *testing.T) {
	for _, request := range []string{`{}`, `{"tools":null,"tool_choice":null,"parallel_tool_calls":null}`} {
		response, err := newResponsesResponse("resp_known", "model", 123, []byte(request))
		if err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"id": `"resp_known"`, "created_at": "123", "output": "[]", "tools": "[]", "tool_choice": `"auto"`, "parallel_tool_calls": "true"} {
			if string(fields[key]) != want {
				t.Errorf("%s from %s = %s, want %s", key, request, fields[key], want)
			}
		}
		for _, key := range []string{"store", "reasoning", "text", "user", "prompt_cache_key", "prompt_cache_retention", "safety_identifier", "service_tier"} {
			if _, ok := fields[key]; ok {
				t.Errorf("invented absent setting %s: %s", key, out)
			}
		}
	}
}

func TestBufferedResponsesItemIdentityAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		convert    func([]byte, string) ([]byte, error)
	}{
		{"chat", `{"id":"reply","choices":[{"finish_reason":"length","message":{"reasoning_content":"reason","content":"partial","refusal":"","tool_calls":[{"id":"reply_item_0","type":"function","function":{"name":"f","arguments":"{\"x\":"}}]}}]}`, ResponsesFromChat},
		{"messages", `{"id":"reply","stop_reason":"max_tokens","content":[{"type":"thinking","thinking":"reason"},{"type":"text","text":"partial"},{"type":"tool_use","id":"reply_item_0","name":"f","input":{}}]}`, ResponsesFromAnthropic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.convert([]byte(tc.body), "model")
			if err != nil {
				t.Fatal(err)
			}
			var response responsesResponse
			if err := json.Unmarshal(out, &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != "incomplete" || len(response.Output) < 3 {
				t.Fatalf("response = %s", out)
			}
			ids := map[string]bool{}
			var sawTool bool
			for _, item := range response.Output {
				if item.ID == "" || ids[item.ID] {
					t.Fatalf("missing/duplicate output item ID: %s", out)
				}
				ids[item.ID] = true
				if item.Type == "message" || item.Type == "function_call" {
					if item.Status != "incomplete" {
						t.Errorf("%s status = %q", item.Type, item.Status)
					}
				}
				if item.Type == "function_call" {
					sawTool = true
					if item.CallID != "reply_item_0" || item.ID == item.CallID {
						t.Errorf("tool item identity = %+v", item)
					}
				}
			}
			if !sawTool {
				t.Fatal("tool call disappeared")
			}
		})
	}
}

func TestResponsesOutputItemShapes(t *testing.T) {
	for _, tc := range []struct {
		item responsesOutputItem
		want string
	}{
		{responsesOutputItem{ID: "m", Type: "message", Role: "assistant", Status: "completed", Name: "unrelated"}, `{"id":"m","type":"message","role":"assistant","status":"completed","content":[]}`},
		{responsesOutputItem{ID: "r", Type: "reasoning", Role: "unrelated"}, `{"id":"r","type":"reasoning","summary":[]}`},
		{responsesOutputItem{ID: "f", Type: "function_call", CallID: "c", Name: "fn", Status: "incomplete", Content: []responsesOutputContent{{Type: "output_text"}}}, `{"id":"f","type":"function_call","status":"incomplete","call_id":"c","name":"fn","arguments":""}`},
	} {
		out, err := json.Marshal(tc.item)
		if err != nil {
			t.Fatal(err)
		}
		var actual, expected any
		if err := json.Unmarshal(out, &actual); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc.want), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("%s = %s, want %s", tc.item.Type, out, tc.want)
		}
	}
}
