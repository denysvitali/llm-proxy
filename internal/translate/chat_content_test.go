package translate

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOpenAIMessageOutContentShapes(t *testing.T) {
	tests := []struct {
		name, input, content, reasoning string
		toolCalls                       int
	}{
		{"plain", `{"content":"answer","reasoning_content":"reason"}`, "answer", "reason", 0},
		{"null", `{"content":null}`, "", "", 0},
		{"missing", `{}`, "", "", 0},
		{"chunks", `{"content":[{"type":"thinking","thinking":[{"type":"text","text":"first "},{"type":"text","text":"second"}]},{"type":"text","text":"an"},{"type":"text","text":"swer"}]}`, "answer", "first second", 0},
		{"explicit reasoning and tools", `{"content":[{"type":"thinking","thinking":[{"type":"text","text":"nested"}]}],"reasoning_content":"explicit ","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`, "", "explicit nested", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var message openAIMessageOut
			if err := json.Unmarshal([]byte(tt.input), &message); err != nil {
				t.Fatal(err)
			}
			if message.Content != tt.content || message.ReasoningContent != tt.reasoning || len(message.ToolCalls) != tt.toolCalls {
				t.Fatalf("decoded message = %+v", message)
			}
		})
	}
}

func TestOpenAIMessageOutRejectsMalformedContent(t *testing.T) {
	for _, input := range []string{
		`{"content":42}`,
		`{"content":{}}`,
		`{"content":[{"type":"text","text":42}]}`,
		`{"content":[{"type":"thinking","thinking":"bad"}]}`,
	} {
		var message openAIMessageOut
		if err := json.Unmarshal([]byte(input), &message); err == nil {
			t.Errorf("accepted malformed content: %s", input)
		}
	}
}

func TestResponsesChatContentShapes(t *testing.T) {
	var message chatMessageOut
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"thought"}]},{"type":"text","text":"answer"}],"reasoning_content":"earlier ","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`), &message); err != nil {
		t.Fatal(err)
	}
	if message.Role != "assistant" || message.Content != "answer" || message.ReasoningContent != "earlier thought" || len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("message = %+v", message)
	}
	for _, tt := range []struct {
		input string
		want  *string
	}{
		{`{"content":null}`, nil},
		{`{}`, nil},
		{`{"content":""}`, new(string)},
		{`{"content":[{"type":"text","text":"answer"}]}`, ptrString("answer")},
	} {
		var delta chatDeltaOut
		if err := json.Unmarshal([]byte(tt.input), &delta); err != nil {
			t.Fatal(err)
		}
		if (delta.Content == nil) != (tt.want == nil) || delta.Content != nil && *delta.Content != *tt.want {
			t.Errorf("delta content from %s = %v, want %v", tt.input, delta.Content, tt.want)
		}
	}
	for _, target := range []any{new(chatMessageOut), new(chatDeltaOut)} {
		if err := json.Unmarshal([]byte(`{"content":[{"type":"text","text":42}]}`), target); err == nil {
			t.Errorf("%T accepted malformed content", target)
		}
	}
}

func ptrString(value string) *string { return &value }

func TestFromOpenAIMistralChunks(t *testing.T) {
	input := []byte(`{"id":"chatcmpl_1","model":"mistral","choices":[{"finish_reason":"tool_calls","message":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"check"}]},{"type":"text","text":"result"}],"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"x\":1}"}}]}}],"usage":{"prompt_tokens":9,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":2}}}`)
	for _, includeThinking := range []bool{false, true} {
		output, err := FromOpenAI(input, "fallback", includeThinking)
		if includeThinking {
			if !errors.Is(err, errUnsignedThinking) || len(output) != 0 {
				t.Fatalf("unsigned reasoning was presented as thinking: %s, %v", output, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var message AnthropicMessageOut
		if err := json.Unmarshal(output, &message); err != nil {
			t.Fatal(err)
		}
		if len(message.Content) != 2 {
			t.Fatalf("content = %#v", message.Content)
		}
		if message.Content[0]["text"] != "result" || message.Content[1]["type"] != "tool_use" {
			t.Fatalf("content = %#v", message.Content)
		}
		if message.StopReason == nil || *message.StopReason != "tool_use" || message.Usage.InputTokens != 7 || message.Usage.OutputTokens != 4 || message.Usage.CacheReadInputTokens != 2 {
			t.Fatalf("stop/usage = %v/%+v", message.StopReason, message.Usage)
		}
	}
}

func TestStreamWriterMistralChunkTransition(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"first "}]}]}}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"second"}]},{"type":"text","text":"an"}]}}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"swer"}}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":2}}}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var output bytes.Buffer
	if err := NewStreamWriter(&output, nil, "mistral", false).Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		`"text":"an"`, `"text":"swer"`,
		`"id":"toolu_call_1"`, `"stop_reason":"tool_use"`, `"cache_read_input_tokens":2`,
		`"input_tokens":7`, `"output_tokens":4`, `event: message_stop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stream missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"type":"thinking"`) || strings.Contains(got, `signature_delta`) {
		t.Fatal("unsigned thinking state was fabricated")
	}
}

func TestResponsesFromChatMistralChunks(t *testing.T) {
	input := []byte(`{"id":"chatcmpl_1","model":"mistral","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"check"}]},{"type":"text","text":"result"}],"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"x\":1}"}}]}}],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`)
	output, err := ResponsesFromChat(input, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 3 {
		t.Fatalf("output = %+v", response.Output)
	}
	if response.Output[0].Type != "reasoning" || len(response.Output[0].Summary) != 1 || response.Output[0].Summary[0].Text != "check" {
		t.Fatalf("reasoning = %+v", response.Output[0])
	}
	if response.Output[1].Type != "message" || len(response.Output[1].Content) != 1 || response.Output[1].Content[0].Text != "result" || response.Output[2].Type != "function_call" || response.Output[2].Name != "lookup" || response.Output[2].Arguments != `{"x":1}` {
		t.Fatalf("output = %+v", response.Output)
	}
	if response.Status != "completed" || response.Model != "mistral" || response.Usage.InputTokens != 9 || response.Usage.OutputTokens != 4 || response.Usage.TotalTokens != 13 {
		t.Fatalf("status/model/usage = %s/%s/%+v", response.Status, response.Model, response.Usage)
	}
}

func TestResponsesStreamFromChatMistralChunkTransition(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl_1","model":"mistral","choices":[{"delta":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"first "}]}]}}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"second"}]},{"type":"text","text":"an"}]}}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"swer"}}]}`,
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var output bytes.Buffer
	if err := NewResponsesStreamFromChat(&output, nil, "fallback").Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		`"delta":"first "`, `"delta":"second"`, `"delta":"an"`, `"delta":"swer"`,
		`"type":"function_call"`, `"name":"lookup"`, `"arguments":"{}"`,
		`"input_tokens":9`, `"output_tokens":4`, `"total_tokens":13`, `event: response.completed`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stream missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "event: response.completed") != 1 {
		t.Fatalf("completion count = %d", strings.Count(got, "event: response.completed"))
	}
	if strings.Index(got, `"delta":"second"`) > strings.Index(got, `"delta":"an"`) {
		t.Fatal("transition emitted answer before reasoning")
	}
}

func TestChatDeltaContentPresence(t *testing.T) {
	for _, tc := range []struct {
		body    string
		present bool
	}{
		{`{}`, false}, {`{"content":null}`, false},
		{`{"content":""}`, true}, {`{"content":[]}`, true},
	} {
		var delta chatDeltaOut
		if err := json.Unmarshal([]byte(tc.body), &delta); err != nil {
			t.Fatal(err)
		}
		if (delta.Content != nil) != tc.present {
			t.Errorf("content presence for %s = %v", tc.body, delta.Content != nil)
		}
	}
}
