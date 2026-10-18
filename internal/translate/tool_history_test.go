package translate

import (
	"encoding/json"
	"testing"
)

func TestTranslatedHistoryIdentity(t *testing.T) {
	for _, tc := range []struct {
		name           string
		calls, results []string
		valid          bool
	}{
		{"parallel results in reverse order", []string{"a", "b"}, []string{"b", "a"}, true},
		{"missing call id", []string{""}, nil, false},
		{"duplicate call", []string{"a", "a"}, nil, false},
		{"orphan result", nil, []string{"a"}, false},
		{"duplicate result", []string{"a"}, []string{"a", "a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chat []chatMessageIn
			var anthropic []AnthropicMessage
			var responses []responsesInputItem
			for _, id := range tc.calls {
				call := chatToolCallIn{ID: id, Type: "function"}
				call.Function.Name, call.Function.Arguments = "test", "{}"
				chat = append(chat, chatMessageIn{Role: "assistant", ToolCalls: []chatToolCallIn{call}})
				anthropic = append(anthropic, AnthropicMessage{Role: "assistant", Content: mustMarshal([]anthropicBlock{{Type: "tool_use", ID: id, Name: "test", Input: json.RawMessage(`{}`)}})})
				responses = append(responses, responsesInputItem{Type: "function_call", CallID: id, Name: "test", Arguments: "{}"})
			}
			for _, id := range tc.results {
				chat = append(chat, chatMessageIn{Role: "tool", ToolCallID: id, Content: mustMarshal("ok")})
				anthropic = append(anthropic, AnthropicMessage{Role: "user", Content: mustMarshal([]anthropicBlock{{Type: "tool_result", ToolUseID: id, Content: mustMarshal("ok")}})})
				responses = append(responses, responsesInputItem{Type: "function_call_output", CallID: id, Output: mustMarshal("ok")})
			}
			for name, err := range map[string]error{
				"chat":      validateChatHistory(chat),
				"anthropic": validateAnthropicHistory(anthropic),
				"responses": validateResponsesHistory(responses),
			} {
				if (err == nil) != tc.valid {
					t.Errorf("%s: error = %v, valid = %t", name, err, tc.valid)
				}
			}
		})
	}
}

func TestResponsesHistoryRejectsMismatchedToolKind(t *testing.T) {
	if err := validateResponsesHistory([]responsesInputItem{
		{Type: "custom_tool_call", CallID: "a"},
		{Type: "function_call_output", CallID: "a"},
	}); err == nil {
		t.Fatal("accepted a function result for a custom tool call")
	}
}
