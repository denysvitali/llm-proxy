package translate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBufferedResponsesTerminalStates(t *testing.T) {
	for _, tc := range []struct {
		name, fields, chatFinish, messagesStop string
		wantError                              bool
	}{
		{name: "completed", fields: `"status":"completed"`, chatFinish: "stop", messagesStop: "end_turn"},
		{name: "omitted", fields: `"id":"resp_1"`, chatFinish: "stop", messagesStop: "end_turn"},
		{name: "token limit", fields: `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`, chatFinish: "length", messagesStop: "max_tokens"},
		{name: "context limit", fields: `"status":"incomplete","incomplete_details":{"reason":"model_context_window_exceeded"}`, chatFinish: "length", messagesStop: "model_context_window_exceeded"},
		{name: "filter", fields: `"status":"incomplete","incomplete_details":{"reason":"content_filter"}`, chatFinish: "content_filter", messagesStop: "refusal"},
		{name: "missing reason", fields: `"status":"incomplete"`, wantError: true},
		{name: "null details", fields: `"status":"incomplete","incomplete_details":null`, wantError: true},
		{name: "unknown reason", fields: `"status":"incomplete","incomplete_details":{"reason":"other"}`, wantError: true},
		{name: "failed", fields: `"status":"failed"`, wantError: true},
		{name: "cancelled", fields: `"status":"cancelled"`, wantError: true},
		{name: "queued", fields: `"status":"queued"`, wantError: true},
		{name: "in progress", fields: `"status":"in_progress"`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{` + tc.fields + `,"output":[{"type":"message","content":[{"type":"output_text","text":""}]}]}`)
			for _, destination := range []string{"chat", "messages"} {
				t.Run(destination, func(t *testing.T) {
					var out []byte
					var err error
					if destination == "chat" {
						out, err = ChatFromResponses(body, "model")
					} else {
						out, err = FromResponses(body, "model", false)
					}
					if tc.wantError {
						if err == nil || len(out) != 0 {
							t.Fatalf("expected no output and an error, got %s, %v", out, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if destination == "chat" {
						var response chatResponseOut
						if err := json.Unmarshal(out, &response); err != nil {
							t.Fatal(err)
						}
						if response.Choices[0].FinishReason != tc.chatFinish {
							t.Fatalf("finish = %q, want %q", response.Choices[0].FinishReason, tc.chatFinish)
						}
					} else {
						var response AnthropicMessageOut
						if err := json.Unmarshal(out, &response); err != nil {
							t.Fatal(err)
						}
						if response.StopReason == nil || *response.StopReason != tc.messagesStop {
							t.Fatalf("stop = %v, want %q", response.StopReason, tc.messagesStop)
						}
					}
				})
			}
		})
	}
}

func TestBufferedChatChoiceCount(t *testing.T) {
	for _, choices := range []string{`[]`, `[{"message":{"content":"one"}},{"message":{"content":"two"}}]`} {
		for name, convert := range map[string]func([]byte, string) ([]byte, error){
			"responses": ResponsesFromChat,
			"messages":  func(body []byte, model string) ([]byte, error) { return FromOpenAI(body, model, false) },
		} {
			t.Run(name+choices, func(t *testing.T) {
				out, err := convert([]byte(`{"choices":`+choices+`}`), "model")
				if err == nil || len(out) != 0 {
					t.Fatalf("expected unsupported choice count error, got %s, %v", out, err)
				}
			})
		}
	}
}

func TestBufferedTerminalBoundaries(t *testing.T) {
	for _, stop := range []string{"pause_turn", "unknown"} {
		body := []byte(fmt.Sprintf(`{"stop_reason":%q,"content":[{"type":"text","text":"partial"}]}`, stop))
		for name, convert := range map[string]func([]byte, string) ([]byte, error){"chat": ChatFromAnthropic, "responses": ResponsesFromAnthropic} {
			t.Run("messages/"+stop+"/"+name, func(t *testing.T) {
				if out, err := convert(body, "model"); err == nil || len(out) != 0 {
					t.Fatalf("expected unsupported stop error, got %s, %v", out, err)
				}
			})
		}
	}
	for _, finish := range []string{"function_call", "unknown", "error"} {
		body := []byte(fmt.Sprintf(`{"choices":[{"finish_reason":%q,"message":{"content":"partial"}}]}`, finish))
		for name, convert := range map[string]func([]byte, string) ([]byte, error){
			"responses": ResponsesFromChat,
			"messages":  func(body []byte, model string) ([]byte, error) { return FromOpenAI(body, model, false) },
		} {
			t.Run("chat/"+finish+"/"+name, func(t *testing.T) {
				if out, err := convert(body, "model"); err == nil || len(out) != 0 {
					t.Fatalf("expected unsupported finish error, got %s, %v", out, err)
				}
			})
		}
	}
	for name, convert := range map[string]func([]byte, string) ([]byte, error){"chat": ChatFromAnthropic, "responses": ResponsesFromAnthropic} {
		t.Run("messages/error/"+name, func(t *testing.T) {
			if out, err := convert([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"unavailable"}}`), "model"); err == nil || len(out) != 0 {
				t.Fatalf("expected upstream error, got %s, %v", out, err)
			}
		})
	}
}

func TestBufferedRefusalRoundTrips(t *testing.T) {
	for _, text := range []string{"", "  cannot comply\n"} {
		t.Run(fmt.Sprintf("text=%q", text), func(t *testing.T) {
			quoted, _ := json.Marshal(text)
			messages := []byte(`{"stop_reason":"refusal","content":[{"type":"text","text":` + string(quoted) + `}]}`)
			chat, err := ChatFromAnthropic(messages, "model")
			if err != nil {
				t.Fatal(err)
			}
			var c chatResponseOut
			if err := json.Unmarshal(chat, &c); err != nil {
				t.Fatal(err)
			}
			if c.Choices[0].Message.Refusal == nil || *c.Choices[0].Message.Refusal != text || c.Choices[0].FinishReason != "content_filter" {
				t.Fatalf("chat refusal = %s", chat)
			}
			responses, err := ResponsesFromAnthropic(messages, "model")
			if err != nil {
				t.Fatal(err)
			}
			var r responsesResponse
			if err := json.Unmarshal(responses, &r); err != nil {
				t.Fatal(err)
			}
			if r.Status != "completed" || len(r.Output) != 1 || len(r.Output[0].Content) != 1 || r.Output[0].Content[0].Type != "refusal" || r.Output[0].Content[0].Refusal != text {
				t.Fatalf("responses refusal = %s", responses)
			}
			for _, source := range []string{"chat", "responses"} {
				var restored []byte
				if source == "chat" {
					restored, err = FromOpenAI(chat, "model", false)
				} else {
					restored, err = FromResponses(responses, "model", false)
				}
				if err != nil {
					t.Fatal(err)
				}
				var m AnthropicMessageOut
				if err := json.Unmarshal(restored, &m); err != nil {
					t.Fatal(err)
				}
				var actual strings.Builder
				for _, block := range m.Content {
					actual.WriteString(block["text"].(string))
				}
				if m.StopReason == nil || *m.StopReason != "refusal" || actual.String() != text {
					t.Fatalf("%s roundtrip = %s", source, restored)
				}
			}
		})
	}
}

func TestResponsesOutputContentShape(t *testing.T) {
	for _, tc := range []struct {
		part responsesOutputContent
		want string
	}{
		{responsesOutputContent{Type: "output_text"}, `{"type":"output_text","text":"","annotations":[]}`},
		{responsesOutputContent{Type: "refusal"}, `{"type":"refusal","refusal":""}`},
		{responsesOutputContent{Type: "summary_text"}, `{"type":"summary_text","text":""}`},
	} {
		out, err := json.Marshal(tc.part)
		if err != nil || string(out) != tc.want {
			t.Fatalf("%s = %s, %v; want %s", tc.part.Type, out, err, tc.want)
		}
	}
}

func TestBufferedRefusalWithTools(t *testing.T) {
	refusal := `{"type":"message","content":[{"type":"refusal","refusal":"no"}]}`
	call := `{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}`
	for _, items := range []string{refusal + "," + call, call + "," + refusal} {
		out, err := FromResponses([]byte(`{"status":"completed","output":[`+items+`]}`), "model", false)
		if err != nil {
			t.Fatal(err)
		}
		var message AnthropicMessageOut
		if err := json.Unmarshal(out, &message); err != nil {
			t.Fatal(err)
		}
		if message.StopReason == nil || *message.StopReason != "refusal" || len(message.Content) != 2 {
			t.Fatalf("refusal must not depend on item order: %s", out)
		}
	}
}

func TestBufferedExactConcatenation(t *testing.T) {
	messages := []byte(`{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":" a "},{"type":"thinking","thinking":"b\n"},{"type":"text","text":" x "},{"type":"text","text":""},{"type":"text","text":"y\n"}]}`)
	responses := []byte(`{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":" a "},{"type":"summary_text","text":"b\n"}]},{"type":"message","content":[{"type":"output_text","text":" x "},{"type":"output_text","text":""},{"type":"output_text","text":"y\n"}]}]}`)
	for source, convert := range map[string]func([]byte, string) ([]byte, error){"messages": ChatFromAnthropic, "responses": ChatFromResponses} {
		t.Run(source, func(t *testing.T) {
			body := messages
			if source == "responses" {
				body = responses
			}
			out, err := convert(body, "model")
			if err != nil {
				t.Fatal(err)
			}
			var chat chatResponseOut
			if err := json.Unmarshal(out, &chat); err != nil {
				t.Fatal(err)
			}
			message := chat.Choices[0].Message
			if message.Content != " x y\n" || message.ReasoningContent != " a b\n" {
				t.Fatalf("message = %+v", message)
			}
		})
	}
}
