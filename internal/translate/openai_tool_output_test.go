package translate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestOpenAIToolOutputRoundTrip(t *testing.T) {
	for _, content := range []string{`""`, `"  \n "`, `[]`, `[{"type":"text","text":""},{"type":"text","text":"first"},{"type":"text","text":" second "}]`} {
		t.Run(content, func(t *testing.T) {
			body := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":` + content + `}]}`)
			responses, err := ChatToResponses(body, "m")
			if err != nil {
				t.Fatal(err)
			}
			chat, err := ResponsesToChat(responses, "m")
			if err != nil {
				t.Fatal(err)
			}
			var got openAIRequest
			if err := json.Unmarshal(chat, &got); err != nil {
				t.Fatal(err)
			}
			var want any
			if err := json.Unmarshal([]byte(content), &want); err != nil {
				t.Fatal(err)
			}
			if len(got.Messages) != 2 || !reflect.DeepEqual(got.Messages[1].Content, want) {
				t.Fatalf("tool result changed: %s", chat)
			}
		})
	}
}

func TestOpenAIToolOutputBoundaries(t *testing.T) {
	for _, output := range []string{`null`, `{}`, `[{"type":"input_image","image_url":"https://example.invalid/i.png"}]`, `[{"type":"input_file","file_id":"file_1"}]`, `[{"type":"input_text"}]`, `[{"type":"input_text","text":null}]`, `[{"type":"input_text","text":"ok","extra":true}]`} {
		body := []byte(`{"input":[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":` + output + `}]}`)
		if out, err := ResponsesToChat(body, "m"); err == nil {
			t.Errorf("unsupported output accepted: %s", out)
		}
	}
}

func TestResponsesToChatGroupsAdjacentCalls(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","call_id":"a","name":"f","arguments":"{}"},
		{"type":"function_call","call_id":"b","name":"g","arguments":"{}"},
		{"type":"function_call_output","call_id":"a","output":""},
		{"type":"function_call_output","call_id":"b","output":""},
		{"role":"user","content":"next"},
		{"type":"function_call","call_id":"c","name":"h","arguments":"{}"}
	]}`)
	out, err := ResponsesToChat(body, "m")
	if err != nil {
		t.Fatal(err)
	}
	var got openAIRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 5 || len(got.Messages[0].ToolCalls) != 2 || len(got.Messages[4].ToolCalls) != 1 {
		t.Fatalf("wrong grouping: %s", out)
	}
	if got.Messages[0].ToolCalls[0].ID != "a" || got.Messages[0].ToolCalls[1].ID != "b" || got.Messages[4].ToolCalls[0].ID != "c" {
		t.Fatalf("call order changed: %s", out)
	}
}
