package translate

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestBufferedResponseCallIdentities(t *testing.T) {
	for _, ids := range [][]string{{"", ""}, {"a", "b"}, {"a", "a"}} {
		chatCalls, responseCalls, anthropicCalls := []any{}, []any{}, []any{}
		for _, id := range ids {
			chatCalls = append(chatCalls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": "test", "arguments": "{}"}})
			responseCalls = append(responseCalls, map[string]any{"type": "function_call", "call_id": id, "name": "test", "arguments": "{}"})
			anthropicCalls = append(anthropicCalls, map[string]any{"type": "tool_use", "id": id, "name": "test", "input": map[string]any{}})
		}
		chat := mustMarshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": chatCalls}, "finish_reason": "tool_calls"}}})
		responses := mustMarshal(map[string]any{"status": "completed", "output": responseCalls})
		anthropic := mustMarshal(map[string]any{"content": anthropicCalls, "stop_reason": "tool_use"})
		for _, tc := range []struct {
			name string
			body []byte
			fn   func([]byte, string) ([]byte, error)
		}{
			{"chat-responses", chat, ResponsesFromChat},
			{"chat-anthropic", chat, func(b []byte, m string) ([]byte, error) { return FromOpenAI(b, m, false) }},
			{"responses-chat", responses, ChatFromResponses},
			{"responses-anthropic", responses, func(b []byte, m string) ([]byte, error) { return FromResponses(b, m, false) }},
			{"anthropic-chat", anthropic, ChatFromAnthropic},
			{"anthropic-responses", anthropic, ResponsesFromAnthropic},
		} {
			t.Run(fmt.Sprintf("%s/%q", tc.name, ids), func(t *testing.T) {
				got, err := tc.fn(tc.body, "model")
				if ids[0] != "" && ids[0] == ids[1] {
					if err == nil {
						t.Fatalf("duplicate identities accepted: %s", got)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Content []struct{ ID string } `json:"content"`
					Output  []struct {
						ID     string `json:"id"`
						CallID string `json:"call_id"`
					} `json:"output"`
					Choices []struct {
						Message struct {
							ToolCalls []struct{ ID string } `json:"tool_calls"`
						} `json:"message"`
					} `json:"choices"`
				}
				if err := json.Unmarshal(got, &result); err != nil {
					t.Fatal(err)
				}
				var actual []string
				for _, b := range result.Content {
					actual = append(actual, b.ID)
				}
				for _, b := range result.Output {
					if b.ID == "" {
						t.Fatal("empty item ID")
					}
					actual = append(actual, b.CallID)
				}
				for _, c := range result.Choices {
					for _, b := range c.Message.ToolCalls {
						actual = append(actual, b.ID)
					}
				}
				if len(actual) != 2 || actual[0] == "" || actual[1] == "" || actual[0] == actual[1] {
					t.Fatalf("invalid identities: %s", got)
				}
			})
		}
	}
}

func TestResponseCallIDsRejectPrefixCollision(t *testing.T) {
	ids := responseCallIDs{}
	if _, err := ids.resolve("a", "toolu_"); err != nil {
		t.Fatal(err)
	}
	if _, err := ids.resolve("toolu_a", "toolu_"); err == nil {
		t.Fatal("prefix collision accepted")
	}
}
