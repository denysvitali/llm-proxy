package translate

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestUnsignedReasoningStreamBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, reasoning, prefix, terminal string
		responses                         bool
	}{
		{"chat", `{"choices":[{"delta":{"reasoning_content":"reason"}}]}`, `{"id":"chat_1","choices":[{"delta":{"content":"answer"}}]}`, `[DONE]`, false},
		{"responses delta", `{"type":"response.reasoning_text.delta","delta":"reason"}`, `{"type":"response.output_text.delta","delta":"answer"}`, `{"type":"response.completed","response":{"id":"resp_1","status":"completed"}}`, true},
		{"responses summary delta", `{"type":"response.reasoning_summary_text.delta","delta":"reason"}`, `{"type":"response.created","response":{"id":"resp_1"}}`, `{"type":"response.completed","response":{"id":"resp_1","status":"completed"}}`, true},
		{"responses item", `{"type":"response.output_item.done","item":{"type":"reasoning","summary":[{"type":"summary_text","text":"reason"}]}}`, `{"type":"response.created","response":{"id":"resp_1"}}`, `{"type":"response.completed","response":{"id":"resp_1","status":"completed"}}`, true},
		{"responses terminal", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"reason"}]}]}}`, `{"type":"response.output_text.delta","delta":"answer"}`, ``, true},
	} {
		for _, started := range []bool{false, true} {
			name := tc.name + "/before output"
			if started {
				name = tc.name + "/after output"
			}
			t.Run(name, func(t *testing.T) {
				var output bytes.Buffer
				input := "data: " + tc.reasoning + "\n\n"
				if started {
					input = "data: " + tc.prefix + "\n\n" + input
				}
				input += "data: " + tc.terminal + "\n\n"
				var err error
				if tc.responses {
					writer := NewResponsesStreamWriter(&output, nil, "model", true)
					err = writer.Consume(strings.NewReader(input))
					writer.Fail("unsigned reasoning")
					writer.Fail("unsigned reasoning")
				} else {
					writer := NewStreamWriter(&output, nil, "model", true)
					err = writer.Consume(strings.NewReader(input))
					writer.Fail("unsigned reasoning")
					writer.Fail("unsigned reasoning")
				}
				if !errors.Is(err, errUnsignedThinking) {
					t.Fatalf("Consume = %v, want unsigned reasoning error", err)
				}
				if !started {
					if output.Len() != 0 {
						t.Fatalf("first-event error committed output: %s", output.String())
					}
					return
				}
				errors := 0
				for _, event := range parseSSE(t, output.String()) {
					switch event.Name {
					case "error":
						errors++
					case "message_stop", "message_delta":
						t.Fatalf("failed stream completed: %s", output.String())
					case "content_block_start":
						if jstr(t, jmap(t, event.Data["content_block"], "block")["type"]) == "thinking" {
							t.Fatal("fabricated thinking block")
						}
					case "content_block_delta":
						kind := jstr(t, jmap(t, event.Data["delta"], "delta")["type"])
						if kind == "thinking_delta" || kind == "signature_delta" {
							t.Fatal("fabricated thinking state")
						}
					}
				}
				if errors != 1 {
					t.Fatalf("error count = %d, want 1", errors)
				}
			})
		}
	}
}
