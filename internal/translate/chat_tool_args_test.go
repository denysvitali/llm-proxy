package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const largeToolInteger = "123456789012345678901234567890"

func TestDecodeChatToolArguments(t *testing.T) {
	for _, tt := range []struct {
		name, input, want string
		valid             bool
	}{
		{"object", `{"n": 123456789012345678901234567890, "text": "a"}`, `{"n":123456789012345678901234567890,"text":"a"}`, true},
		{"partial string", `"{\"n\":"`, `{"n":`, true},
		{"null", `null`, "", true},
		{"omitted", ``, "", true},
		{"number", `12`, "", false},
		{"array", `[]`, "", false},
		{"boolean", `true`, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeChatToolArguments(json.RawMessage(tt.input))
			if (err == nil) != tt.valid || got != tt.want {
				t.Fatalf("arguments = %q, err = %v", got, err)
			}
		})
	}
}

func TestChatToolCallFieldsAndOptionalArguments(t *testing.T) {
	for _, arguments := range []string{`null`, ``, `""`, `{"n":123456789012345678901234567890}`} {
		field := ""
		if arguments != "" {
			field = `,"arguments":` + arguments
		}
		payload := `{"index":3,"id":"call_3","type":"function","function":{"name":"lookup"` + field + `}}`
		var call openAIToolCall
		if err := json.Unmarshal([]byte(payload), &call); err != nil {
			t.Fatal(err)
		}
		if call.Index != 3 || call.ID != "call_3" || call.Type != "function" || call.Function.Name != "lookup" {
			t.Fatalf("tool call fields = %+v", call)
		}
		if arguments == `{"n":123456789012345678901234567890}` {
			if call.Function.Arguments != arguments {
				t.Fatalf("object arguments = %q", call.Function.Arguments)
			}
		} else if call.Function.Arguments != "" {
			t.Fatalf("optional arguments = %q", call.Function.Arguments)
		}
		var function chatFuncOut
		if err := json.Unmarshal([]byte(`{"name":"lookup"`+field+`}`), &function); err != nil {
			t.Fatal(err)
		}
		if function.Name != "lookup" || function.Arguments != call.Function.Arguments {
			t.Fatalf("Responses function = %+v", function)
		}
	}
}

func TestMistralObjectToolArgumentsBufferedTranslations(t *testing.T) {
	input := []byte(`{"id":"chatcmpl_1","choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":[{"index":3,"id":"call_3","type":"function","function":{"name":"lookup","arguments":{"n":123456789012345678901234567890}}}]}}]}`)
	anthropic, err := FromOpenAI(input, "mistral", false)
	if err != nil {
		t.Fatal(err)
	}
	var message AnthropicMessageOut
	if err := json.Unmarshal(anthropic, &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0]["type"] != "tool_use" || message.Content[0]["id"] != "toolu_call_3" || message.Content[0]["name"] != "lookup" || !bytes.Contains(anthropic, []byte(largeToolInteger)) {
		t.Fatalf("Anthropic output = %s", anthropic)
	}
	responses, err := ResponsesFromChat(input, "mistral")
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(responses, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0].Type != "function_call" || response.Output[0].CallID != "call_3" || response.Output[0].Name != "lookup" || response.Output[0].Arguments != `{"n":123456789012345678901234567890}` {
		t.Fatalf("Responses output = %s", responses)
	}
}

func TestMistralObjectToolArgumentsStreamTranslations(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":3,"id":"call_3","type":"function","function":{"name":"lookup","arguments":{"n":123456789012345678901234567890}}}]}}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var anthropic bytes.Buffer
	if err := NewStreamWriter(&anthropic, nil, "mistral", false).Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"toolu_call_3"`, `"name":"lookup"`, largeToolInteger, `"stop_reason":"tool_use"`, `event: message_stop`} {
		if !strings.Contains(anthropic.String(), want) {
			t.Errorf("Anthropic stream missing %q: %s", want, anthropic.String())
		}
	}
	var responses bytes.Buffer
	if err := NewResponsesStreamFromChat(&responses, nil, "mistral").Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"call_id":"call_3"`, `"name":"lookup"`, largeToolInteger, `event: response.completed`} {
		if !strings.Contains(responses.String(), want) {
			t.Errorf("Responses stream missing %q: %s", want, responses.String())
		}
	}
}

func TestChatToolStringFragmentsRemainExact(t *testing.T) {
	input := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"n\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"123}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var anthropic bytes.Buffer
	if err := NewStreamWriter(&anthropic, nil, "mistral", false).Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(anthropic.String(), `"partial_json":"{\"n\":"`) || !strings.Contains(anthropic.String(), `"partial_json":"123}"`) {
		t.Fatalf("Anthropic fragments changed: %s", anthropic.String())
	}
	var responses bytes.Buffer
	if err := NewResponsesStreamFromChat(&responses, nil, "mistral").Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(responses.String(), `"arguments":"{\"n\":123}"`) {
		t.Fatalf("Responses fragments changed: %s", responses.String())
	}
}
