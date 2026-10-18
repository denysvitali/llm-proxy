package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolSchemaPreservesSemantics(t *testing.T) {
	schema := json.RawMessage(` {"$schema":"https://json-schema.org/draft/2020-12/schema","allOf":[{"properties":{"n":{"const":9007199254740993}}}],"additionalProperties":false} `)
	got, err := sanitizeSchema(schema)
	if err != nil || !bytes.Equal(got, schema) {
		t.Fatalf("schema = %s, error = %v", got, err)
	}
	for _, invalid := range []string{"null", "[]", "true", `"object"`, "{"} {
		if _, err := sanitizeSchema(json.RawMessage(invalid)); err == nil {
			t.Errorf("accepted non-object schema %q", invalid)
		}
	}
}

func TestToolArgumentObjectValidation(t *testing.T) {
	for _, invalid := range []string{"", "null", "[]", "true", "42", `"text"`, "{"} {
		t.Run(invalid, func(t *testing.T) {
			if _, err := argumentsJSON(invalid); err == nil {
				t.Error("accepted invalid tool input")
			}
			if _, err := compactJSON(json.RawMessage(invalid)); err == nil {
				t.Error("compacted invalid tool input")
			}
			call := openAIToolCall{}
			call.Function.Arguments = invalid
			if _, err := toolUseBlock(call); err == nil {
				t.Error("converted invalid tool input")
			}
		})
	}
	input := ` {"large":9007199254740993,"decimal":1.00,"exp":1e3,"label":"1.0"} `
	got, err := compactJSON(json.RawMessage(input))
	if err != nil || got != strings.TrimSpace(input) {
		t.Fatalf("compacted arguments = %s, error = %v", got, err)
	}
}

func TestIncompleteArgumentsRoundTrip(t *testing.T) {
	for _, arguments := range []string{"", `{"partial":`, ` {"n":9007199254740993} `} {
		encoded, _ := json.Marshal(arguments)
		chat := []byte(`{"id":"reply","choices":[{"message":{"tool_calls":[{"id":"call_1","function":{"name":"test","arguments":` + string(encoded) + `}}]},"finish_reason":"length"}]}`)
		converted, err := ResponsesFromChat(chat, "model")
		if err != nil {
			t.Fatal(err)
		}
		var response responsesResponse
		if err := json.Unmarshal(converted, &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != "incomplete" || len(response.Output) != 1 || response.Output[0].Status != "incomplete" || response.Output[0].Arguments != arguments {
			t.Fatalf("incomplete arguments changed: %s", converted)
		}
		restored, err := ChatFromResponses(converted, "model")
		if err != nil {
			t.Fatal(err)
		}
		var result chatResponseOut
		if err := json.Unmarshal(restored, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Choices) != 1 || result.Choices[0].FinishReason != "length" || len(result.Choices[0].Message.ToolCalls) != 1 || result.Choices[0].Message.ToolCalls[0].Function.Arguments != arguments {
			t.Fatalf("incomplete round trip changed: %s", restored)
		}
	}
}

func TestBufferedAnthropicIncompleteArgumentsBoundary(t *testing.T) {
	for _, argument := range []string{"", `{"partial":`, ` {"n":9007199254740993} `} {
		encoded, _ := json.Marshal(argument)
		chat := []byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","function":{"name":"test","arguments":` + string(encoded) + `}}]},"finish_reason":"length"}]}`)
		responses := []byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","call_id":"call_1","name":"test","arguments":` + string(encoded) + `}]}`)
		for _, tc := range []struct {
			name    string
			body    []byte
			convert func([]byte, string, bool) ([]byte, error)
		}{
			{"chat", chat, FromOpenAI},
			{"responses", responses, FromResponses},
		} {
			t.Run(tc.name+"/"+argument, func(t *testing.T) {
				got, err := tc.convert(tc.body, "model", false)
				if !json.Valid([]byte(argument)) {
					if err == nil || !strings.Contains(err.Error(), "complete JSON object") {
						t.Fatalf("truncated object must fail explicitly: %s, %v", got, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					StopReason string `json:"stop_reason"`
					Content    []struct {
						Input json.RawMessage `json:"input"`
					} `json:"content"`
				}
				if err := json.Unmarshal(got, &result); err != nil {
					t.Fatal(err)
				}
				if result.StopReason != "max_tokens" || len(result.Content) != 1 || string(result.Content[0].Input) != strings.TrimSpace(argument) {
					t.Fatalf("incomplete state or input changed: %s", got)
				}
			})
		}
	}
}

func TestBufferedToolResponsesRejectInvalidArguments(t *testing.T) {
	for _, argument := range []string{"", "null", "[]", "{"} {
		encoded, _ := json.Marshal(argument)
		chat := []byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","function":{"name":"test","arguments":` + string(encoded) + `}}]},"finish_reason":"tool_calls"}]}`)
		if _, err := FromOpenAI(chat, "model", false); err == nil {
			t.Errorf("Chat accepted %q", argument)
		}
		response := []byte(`{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"test","arguments":` + string(encoded) + `}]}`)
		if _, err := ResponsesFromChat(chat, "model"); err == nil {
			t.Errorf("Chat to Responses accepted %q", argument)
		}
		if _, err := FromResponses(response, "model", false); err == nil {
			t.Errorf("Responses accepted %q", argument)
		}
		if _, err := ChatFromResponses(response, "model"); err == nil {
			t.Errorf("Responses to Chat accepted %q", argument)
		}
	}
}
