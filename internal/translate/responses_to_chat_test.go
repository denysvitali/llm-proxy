package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesToChatRequest(t *testing.T) {
	in := []byte(`{
		"model": "ignored",
		"instructions": "be terse",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"rome\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"},
			{"type":"message","role":"user","content":"plain string input"}
		],
		"tools": [{"type":"function","name":"get_weather","description":"weather lookup","parameters":{"type":"object"}}],
		"tool_choice": {"type":"function","name":"get_weather"},
		"max_output_tokens": 256,
		"temperature": 0.5,
		"stream": true
	}`)
	out, err := ResponsesToChat(in, "stealth-mock")
	if err != nil {
		t.Fatalf("ResponsesToChat: %v", err)
	}
	var got openAIRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode translated request: %v", err)
	}

	if got.Model != "stealth-mock" {
		t.Errorf("model = %q, want stealth-mock", got.Model)
	}
	if len(got.Messages) != 5 {
		t.Fatalf("messages = %d, want 5 (system + 4 items)", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be terse" {
		t.Errorf("system message = %+v, want instructions", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hello" {
		t.Errorf("first user message = %+v", got.Messages[1])
	}
	if got.Messages[2].Role != "assistant" || len(got.Messages[2].ToolCalls) != 1 ||
		got.Messages[2].ToolCalls[0].ID != "call_1" || got.Messages[2].ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("function_call item became %+v", got.Messages[2])
	}
	if got.Messages[3].Role != "tool" || got.Messages[3].ToolCallID != "call_1" || got.Messages[3].Content != "sunny" {
		t.Errorf("function_call_output item became %+v", got.Messages[3])
	}
	if got.Messages[4].Content != "plain string input" {
		t.Errorf("string-content message = %+v", got.Messages[4])
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tools = %+v", got.Tools)
	}
	choice, ok := got.ToolChoice.(map[string]any)
	if !ok || choice["type"] != "function" {
		t.Fatalf("tool_choice = %#v, want nested function object", got.ToolChoice)
	}
	fn, _ := choice["function"].(map[string]any)
	if fn == nil || fn["name"] != "get_weather" {
		t.Errorf("tool_choice.function = %#v", choice["function"])
	}
	if got.MaxTokens != 256 || got.Temperature == nil || *got.Temperature != 0.5 {
		t.Errorf("sampling fields = max:%d temp:%v", got.MaxTokens, got.Temperature)
	}
	if !got.Stream || got.StreamOptionsUsage == nil || !got.StreamOptionsUsage.IncludeUsage {
		t.Errorf("stream flags: stream=%v options=%+v", got.Stream, got.StreamOptionsUsage)
	}
}

func TestResponsesToChatToolChoiceStrings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want any
	}{
		{`"auto"`, "auto"},
		{`"none"`, "none"},
		{`"required"`, "required"},
		{`"banana"`, nil},
	} {
		out, err := ResponsesToChat([]byte(`{"input":[{"type":"message","role":"user","content":"x"}],"tool_choice":`+tc.in+`}`), "m")
		if err != nil {
			t.Fatalf("ResponsesToChat(%s): %v", tc.in, err)
		}
		var got openAIRequest
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if got.ToolChoice != tc.want {
			t.Errorf("tool_choice %s -> %#v, want %#v", tc.in, got.ToolChoice, tc.want)
		}
	}
}

func TestResponsesToChatRejectsEmptyAndUnknown(t *testing.T) {
	if _, err := ResponsesToChat([]byte(`{"input":[]}`), "m"); err == nil {
		t.Error("empty input accepted")
	}
	if _, err := ResponsesToChat([]byte(`{"input":[{"type":"item_reference","id":"x"}]}`), "m"); err == nil {
		t.Error("unsupported item type accepted")
	}
}

func TestResponsesFromChatResponse(t *testing.T) {
	in := []byte(`{
		"id": "chatcmpl-1",
		"model": "venice-model",
		"choices": [{
			"index": 0,
			"message": {"role":"assistant","content":"E2E_OK"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}
	}`)
	out, err := ResponsesFromChat(in, "stealth-mock")
	if err != nil {
		t.Fatalf("ResponsesFromChat: %v", err)
	}
	var resp responsesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("status = %q", resp.Status)
	}
	if resp.Model != "venice-model" {
		t.Errorf("model = %q, want upstream model echoed", resp.Model)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "message" {
		t.Fatalf("output = %+v", resp.Output)
	}
	if len(resp.Output[0].Content) != 1 || resp.Output[0].Content[0].Text != "E2E_OK" {
		t.Errorf("content = %+v", resp.Output[0].Content)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 7 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestResponsesFromChatToolCallsAndLength(t *testing.T) {
	in := []byte(`{
		"id": "chatcmpl-2",
		"choices": [{
			"message": {"role":"assistant","content":"","tool_calls":[
				{"id":"call_9","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}
			]},
			"finish_reason": "length"
		}],
		"usage": {"prompt_tokens": 3, "completion_tokens": 4}
	}`)
	out, err := ResponsesFromChat(in, "m")
	if err != nil {
		t.Fatalf("ResponsesFromChat: %v", err)
	}
	var resp responsesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "incomplete" || len(resp.IncompleteDetails) == 0 {
		t.Errorf("status = %q details = %s, want incomplete/max_output_tokens", resp.Status, resp.IncompleteDetails)
	}
	var found bool
	for _, item := range resp.Output {
		if item.Type == "function_call" && item.CallID == "call_9" && item.Name == "f" && item.Arguments == `{"a":1}` {
			found = true
		}
	}
	if !found {
		t.Errorf("function_call item missing in output = %+v", resp.Output)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("total tokens = %d, want computed 7", resp.Usage.TotalTokens)
	}
}

func TestResponsesStreamFromChat(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"chatcmpl-s","object":"chat.completion.chunk","model":"mock","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-s","object":"chat.completion.chunk","model":"mock","choices":[{"index":0,"delta":{"content":"E2E"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-s","object":"chat.completion.chunk","model":"mock","choices":[{"index":0,"delta":{"content":"_OK"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-s","object":"chat.completion.chunk","model":"mock","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var out strings.Builder
	w := NewResponsesStreamFromChat(&out, func() {}, "mock")
	if err := w.Consume(strings.NewReader(upstream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	w.Finish()

	body := out.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_item.added",
		`"delta":"E2E"`,
		`"delta":"_OK"`,
		"event: response.output_text.delta",
		"event: response.output_item.done",
		`"text":"E2E_OK"`,
		"event: response.completed",
		`"input_tokens":5`,
		`"output_tokens":2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stream missing %q\nfull:\n%s", want, body)
		}
	}
	if strings.Count(body, "event: response.completed") != 1 {
		t.Errorf("response.completed emitted more than once:\n%s", body)
	}
}

func TestResponsesStreamFromChatToolCall(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"f","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")

	var out strings.Builder
	w := NewResponsesStreamFromChat(&out, func() {}, "mock")
	if err := w.Consume(strings.NewReader(upstream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	w.Finish()
	body := out.String()
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"f"`,
		`"arguments":"{\"x\":1}"`,
		"event: response.function_call_arguments.delta",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stream missing %q\nfull:\n%s", want, body)
		}
	}
}

// Codex opens every turn with a "developer" item carrying its skills and
// permissions preamble. That role only exists in the OpenAI APIs, so it has to
// reach a chat-completions backend as part of the system prompt rather than as
// a message role the upstream will reject.
func TestResponsesToChatFoldsPromptRolesIntoSystem(t *testing.T) {
	in := []byte(`{
		"model": "ignored",
		"instructions": "be terse",
		"input": [
			{"type":"message","role":"developer","content":[
				{"type":"input_text","text":"<skills>a</skills>"},
				{"type":"input_text","text":"<permissions>b</permissions>"}
			]},
			{"type":"message","role":"system","content":"and polite"},
			{"type":"message","role":"user","content":"hello"}
		]
	}`)
	out, err := ResponsesToChat(in, "stealth-mock")
	if err != nil {
		t.Fatalf("ResponsesToChat: %v", err)
	}
	var got openAIRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode translated request: %v", err)
	}

	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(got.Messages))
	}
	want := "be terse\n\n<skills>a</skills>\n\n<permissions>b</permissions>\n\nand polite"
	if got.Messages[0].Role != "system" || got.Messages[0].Content != want {
		t.Errorf("system message = %+v, want %q", got.Messages[0], want)
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hello" {
		t.Errorf("user message = %+v", got.Messages[1])
	}
	for _, message := range got.Messages {
		switch message.Role {
		case "system", "user", "assistant", "tool":
		default:
			t.Errorf("message carries role %q, which chat-completions upstreams reject", message.Role)
		}
	}
}

// A developer item on its own still produces a usable request: its text
// becomes the system prompt instead of vanishing.
func TestResponsesToChatPromptOnlyInputKeepsSystem(t *testing.T) {
	in := []byte(`{"model":"ignored","input":[{"type":"message","role":"developer","content":"rules"}]}`)
	out, err := ResponsesToChat(in, "stealth-mock")
	if err != nil {
		t.Fatalf("ResponsesToChat: %v", err)
	}
	var got openAIRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode translated request: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "system" || got.Messages[0].Content != "rules" {
		t.Fatalf("messages = %+v, want a single system message", got.Messages)
	}
}
