package translate

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestResponsesToolAliases(t *testing.T) {
	request := []byte(`{"tools":[
		{"type":"function","name":"space__run"},
		{"type":"namespace","name":"space","tools":[{"type":"function","name":"run"}]},
		{"type":"namespace","name":"space__","tools":[{"type":"function","name":"run"}]},
		{"type":"function","name":"ordinary"},
		{"type":"function","name":"punctuated.name"},
		{"type":"function","name":"` + strings.Repeat("long", 20) + `"}
	]}`)
	tools, identities, err := responsesTools(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 6 || len(identities) != 6 {
		t.Fatalf("tools=%d identities=%d", len(tools), len(identities))
	}
	_, repeated, err := responsesTools(request)
	if err != nil || !reflect.DeepEqual(identities, repeated) {
		t.Fatalf("unstable aliases: %v, %v", repeated, err)
	}
	for _, tool := range tools {
		if !validFlatToolName(tool.Name) {
			t.Fatalf("invalid wire name %q", tool.Name)
		}
		identity := identities[tool.Name]
		wire, err := responsesWireToolName(identity.Name, identity.Namespace, identities)
		if err != nil || wire != tool.Name {
			t.Fatalf("identity lookup: %q, %v", wire, err)
		}
		name, namespace := restoreResponsesToolName(wire, identities)
		if name != identity.Name || namespace != identity.Namespace {
			t.Fatalf("identity not restored: %q/%q", namespace, name)
		}
	}
	if tools[3].Name != "ordinary" {
		t.Fatalf("unambiguous name changed: %q", tools[3].Name)
	}
	name, namespace := restoreResponsesToolName("run", identities)
	if name != "run" || namespace != "" {
		t.Fatalf("bare name guessed: %q/%q", namespace, name)
	}
	if _, err := responsesWireToolName(tools[0].Name, "", identities); err == nil {
		t.Fatal("historical call impersonated an alias")
	}
}

func TestResponsesToolIdentityAcrossDirections(t *testing.T) {
	for _, namespace := range []string{"", "space", "space__"} {
		t.Run("namespace="+namespace, func(t *testing.T) {
			name := "run"
			if namespace == "" {
				name = "space__run"
			}
			request := []byte(fmt.Sprintf(`{"input":[
				{"role":"user","content":"run"},
				{"type":"function_call","call_id":"call_1","namespace":%q,"name":%q,"arguments":"{}"},
				{"type":"function_call_output","call_id":"call_1","output":"ok"}
			],"tools":[
				{"type":"function","name":"space__run"},
				{"type":"namespace","name":"space","tools":[{"type":"function","name":"run"}]},
				{"type":"namespace","name":"space__","tools":[{"type":"function","name":"run"}]}
			],"tool_choice":{"type":"function","namespace":%q,"name":%q}}`, namespace, name, namespace, name))
			_, identities, err := responsesTools(request)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := responsesWireToolName(name, namespace, identities)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := ResponsesToChat(request, "m")
			if err != nil {
				t.Fatal(err)
			}
			var chatRequest openAIRequest
			if err := json.Unmarshal(chat, &chatRequest); err != nil {
				t.Fatal(err)
			}
			if chatRequest.Messages[1].ToolCalls[0].Function.Name != wire {
				t.Fatalf("chat history = %s", chat)
			}
			choice := chatRequest.ToolChoice.(map[string]any)["function"].(map[string]any)
			if choice["name"] != wire {
				t.Fatalf("chat choice = %#v", choice)
			}
			anthropic, err := ResponsesToAnthropic(request, "m")
			if err != nil {
				t.Fatal(err)
			}
			var anthropicRequest anthropicRequest
			if err := json.Unmarshal(anthropic, &anthropicRequest); err != nil {
				t.Fatal(err)
			}
			var blocks []anthropicBlock
			if err := json.Unmarshal(anthropicRequest.Messages[1].Content, &blocks); err != nil {
				t.Fatal(err)
			}
			if blocks[0].Name != wire || anthropicRequest.ToolChoice.(map[string]any)["name"] != wire {
				t.Fatalf("Messages history/choice = %s", anthropic)
			}
			check := func(raw []byte) {
				t.Helper()
				var response responsesResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Output) != 1 || response.Output[0].Name != name || response.Output[0].Namespace != namespace {
					t.Fatalf("response identity = %s", raw)
				}
			}
			chatResponse := []byte(fmt.Sprintf(`{"choices":[{"message":{"tool_calls":[{"id":"call_2","type":"function","function":{"name":%q,"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, wire))
			out, err := ResponsesFromChatForRequest(chatResponse, "m", request)
			if err != nil {
				t.Fatal(err)
			}
			check(out)
			anthropicResponse := []byte(fmt.Sprintf(`{"content":[{"type":"tool_use","id":"call_2","name":%q,"input":{}}],"stop_reason":"tool_use"}`, wire))
			out, err = ResponsesFromAnthropicForRequest(anthropicResponse, "m", request)
			if err != nil {
				t.Fatal(err)
			}
			check(out)
			chatStream := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_2\",\"function\":{\"name\":%q,\"arguments\":\"{}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", wire)
			anthropicStream := fmt.Sprintf("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_2\",\"name\":%q,\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", wire)
			for _, source := range []string{"chat", "anthropic"} {
				var stream strings.Builder
				if source == "chat" {
					err = NewResponsesStreamFromChatForRequest(&stream, nil, "m", request).Consume(strings.NewReader(chatStream))
				} else {
					err = NewResponsesStreamFromAnthropicForRequest(&stream, nil, "m", request).Consume(strings.NewReader(anthropicStream))
				}
				if err != nil {
					t.Fatal(err)
				}
				completed := 0
				for _, line := range strings.Split(stream.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event struct {
						Type     string          `json:"type"`
						Response json.RawMessage `json:"response"`
					}
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
						t.Fatal(err)
					}
					if event.Type == "response.completed" {
						check(event.Response)
						completed++
					}
				}
				if completed != 1 {
					t.Fatalf("%s completed events = %d", source, completed)
				}
			}
		})
	}
}

func TestResponsesToolCapabilityBoundaries(t *testing.T) {
	for _, convert := range []struct {
		name string
		fn   func([]byte, string) ([]byte, error)
	}{{"chat", ResponsesToChat}, {"anthropic", ResponsesToAnthropic}} {
		t.Run(convert.name, func(t *testing.T) {
			for _, tools := range []string{
				`[{"type":"function","name":"run"},{"type":"function","name":"run"}]`,
				`[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"run"},{"type":"function","name":"run"}]}]`,
				// Client-executed tools (custom, web_search) and unknown function
				// fields degrade instead: the function surface is dropped or
				// cleaned rather than failing the turn (see TestDegradeTools).
				`[{"type":"namespace","name":"ns","tools":[]}]`,
			} {
				request := []byte(`{"input":"run","tools":` + tools + `}`)
				if _, err := convert.fn(request, "m"); err == nil {
					t.Errorf("unsupported declaration accepted: %s", tools)
				}
			}
			for _, input := range []string{
				`[{"type":"reasoning","encrypted_content":"opaque","content":null}]`,
				`[{"type":"reasoning","summary":[{"type":"summary_text","text":"prior reasoning"}]}]`,
				`[{"type":"additional_tools","tools":[{"type":"web_search"}]}]`,
				`[{"type":"custom_tool_call","call_id":"c1","name":"shell","input":"ls"}]`,
				`[{"type":"custom_tool_call","call_id":"c1","name":"shell","input":"{}"},{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]`,
				`[{"type":"function_call","call_id":"c1","namespace":"missing","name":"run","arguments":"{}"}]`,
			} {
				if _, err := convert.fn([]byte(`{"input":`+input+`}`), "m"); err == nil {
					t.Errorf("unsupported history accepted: %s", input)
				}
			}
		})
	}
}
