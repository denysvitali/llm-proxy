package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesToAnthropic(t *testing.T) {
	body := []byte(`{
		"model":"client-r",
		"instructions":"be helpful",
		"max_output_tokens":512,
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"function_call_output","call_id":"c1","output":"result"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"old trace"}]}
		],
		"tools":[{"type":"function","name":"lookup","description":"finds","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"lookup"}
	}`)
	out, err := ResponsesToAnthropic(body, "upstream-r")
	if err != nil {
		t.Fatalf("ResponsesToAnthropic: %v", err)
	}
	got := decodeJSON(t, out)

	if got["model"] != "upstream-r" || got["max_tokens"].(float64) != 512 || got["system"] != "be helpful" {
		t.Errorf("envelope = %v", got)
	}
	messages := jarray(t, got["messages"], "messages")
	if len(messages) != 3 { // user message + assistant function_call + user tool_result
		t.Fatalf("messages = %d entries, want 3 (reasoning dropped)", len(messages))
	}
	callMsg := jmap(t, messages[1], "")
	if callMsg["role"] != "assistant" {
		t.Fatalf("function_call role = %v, want assistant", callMsg["role"])
	}
	callBlock := jmap(t, jarray(t, callMsg["content"], "content")[0], "")
	if callBlock["type"] != "tool_use" || callBlock["id"] != "c1" ||
		jmap(t, callBlock["input"], "input")["q"] != "x" {
		t.Errorf("tool_use block = %v", callBlock)
	}
	resultMsg := jmap(t, messages[2], "")
	resultBlock := jmap(t, jarray(t, resultMsg["content"], "content")[0], "")
	if resultBlock["type"] != "tool_result" || resultBlock["tool_use_id"] != "c1" {
		t.Errorf("tool_result block = %v", resultBlock)
	}
	choice := jmap(t, got["tool_choice"], "tool_choice")
	if choice["type"] != "tool" || choice["name"] != "lookup" {
		t.Errorf("tool_choice = %v, want forced tool", choice)
	}
}

func TestResponsesToAnthropicArrayToolOutputAndAdditionalTools(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"additional_tools","tools":[{"type":"web_search"}]},
			{"type":"message","role":"user","content":"hi"},
			{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"function_call_output","call_id":"c1","output":[
				{"type":"input_text","text":"first"},
				{"type":"input_text","text":"second"}
			]}
		]
	}`)
	out, err := ResponsesToAnthropic(body, "upstream-r")
	if err != nil {
		t.Fatalf("ResponsesToAnthropic: %v", err)
	}
	got := decodeJSON(t, out)
	messages := jarray(t, got["messages"], "messages")
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3 (additional_tools dropped)", len(messages))
	}
	resultMsg := jmap(t, messages[2], "")
	resultBlock := jmap(t, jarray(t, resultMsg["content"], "content")[0], "")
	if resultBlock["type"] != "tool_result" || resultBlock["tool_use_id"] != "c1" {
		t.Errorf("tool_result block = %v", resultBlock)
	}
	if resultBlock["content"] != "first\nsecond" {
		t.Errorf("flattened output = %#v, want first\\nsecond", resultBlock["content"])
	}
}

func TestResponsesToAnthropicStringInput(t *testing.T) {
	out, err := ResponsesToAnthropic([]byte(`{"model":"m","input":"hi"}`), "u")
	if err != nil {
		t.Fatalf("ResponsesToAnthropic: %v", err)
	}
	got := decodeJSON(t, out)
	messages := jarray(t, got["messages"], "messages")
	if len(messages) != 1 {
		t.Fatalf("messages = %d entries, want 1", len(messages))
	}
	msg := jmap(t, messages[0], "")
	block := jmap(t, jarray(t, msg["content"], "content")[0], "")
	if msg["role"] != "user" || block["text"] != "hi" {
		t.Errorf("string input became %v / %v, want one user text message", msg, block)
	}
}

func TestResponsesFromAnthropic(t *testing.T) {
	upstream := []byte(`{
		"id":"msg_r","model":"claude-u",
		"content":[
			{"type":"thinking","thinking":"hmm"},
			{"type":"text","text":"answer"},
			{"type":"tool_use","id":"t9","name":"act","input":{"k":1}}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":6,"cache_read_input_tokens":2}
	}`)
	out, err := ResponsesFromAnthropic(upstream, "client-r")
	if err != nil {
		t.Fatalf("ResponsesFromAnthropic: %v", err)
	}
	got := decodeJSON(t, out)

	if got["id"] != "msg_r" || got["model"] != "claude-u" || got["status"] != "completed" {
		t.Errorf("envelope = %v", got)
	}
	output := jarray(t, got["output"], "output")
	kinds := make([]string, 0, len(output))
	for _, item := range output {
		kinds = append(kinds, jstr(t, jmap(t, item, "")["type"]))
	}
	if strings.Join(kinds, ",") != "reasoning,message,function_call" {
		t.Errorf("output kinds = %v, want reasoning,message,function_call", kinds)
	}
	usage := jmap(t, got["usage"], "usage")
	details := jmap(t, usage["input_tokens_details"], "input_tokens_details")
	if usage["input_tokens"].(float64) != 5 || details["cached_tokens"].(float64) != 2 {
		t.Errorf("usage = %v, want 5 in with 2 cached", usage)
	}

	maxed := []byte(`{"id":"m2","content":[{"type":"text","text":"x"}],"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":2}}`)
	outMax, err := ResponsesFromAnthropic(maxed, "client-r")
	if err != nil {
		t.Fatalf("ResponsesFromAnthropic(maxed): %v", err)
	}
	gotMax := decodeJSON(t, outMax)
	if gotMax["status"] != "incomplete" {
		t.Errorf("status = %v, want incomplete for max_tokens", gotMax["status"])
	}
	incomplete := jmap(t, gotMax["incomplete_details"], "incomplete_details")
	if incomplete["reason"] != "max_output_tokens" {
		t.Errorf("incomplete_details = %v", incomplete)
	}
}

func TestResponsesStreamFromAnthropic(t *testing.T) {
	upstream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_x","model":"u","usage":{"input_tokens":8}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tc","name":"go"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":2}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	var buf bytes.Buffer
	writer := NewResponsesStreamFromAnthropic(&buf, nil, "client-x")
	if err := writer.Consume(strings.NewReader(upstream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	events := parseSSE(t, buf.String())
	names := make([]string, 0, len(events))
	for _, ev := range events {
		names = append(names, ev.Name)
	}
	for _, want := range []string{
		"response.created",
		"response.output_item.added", "response.reasoning_text.delta",
		"response.output_item.added", "response.output_text.delta",
		"response.output_item.added", "response.function_call_arguments.delta",
		"response.completed",
	} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stream missing event %q; got %v\n%s", want, names, buf.String())
		}
	}

	completed := events[len(events)-1]
	response := jmap(t, completed.Data["response"], "response")
	if response["status"] != "completed" {
		t.Errorf("final status = %v", response["status"])
	}
	usage := jmap(t, response["usage"], "usage")
	if usage["input_tokens"].(float64) != 8 || usage["output_tokens"].(float64) != 3 {
		t.Errorf("final usage = %v, want 8 in / 3 out", usage)
	}
	output := jarray(t, response["output"], "output")
	kinds := make([]string, 0, len(output))
	for _, item := range output {
		kinds = append(kinds, jstr(t, jmap(t, item, "")["type"]))
	}
	if strings.Join(kinds, ",") != "reasoning,message,function_call" {
		t.Errorf("collected output kinds = %v", kinds)
	}
}

// Anthropic knows only "user" and "assistant", so Codex's "developer" item has
// to arrive as part of the system prompt.
func TestResponsesToAnthropicFoldsPromptRolesIntoSystem(t *testing.T) {
	in := []byte(`{
		"model": "ignored",
		"instructions": "be terse",
		"input": [
			{"type":"message","role":"developer","content":[
				{"type":"input_text","text":"<skills>a</skills>"},
				{"type":"input_text","text":"<permissions>b</permissions>"}
			]},
			{"type":"message","role":"user","content":"hello"}
		]
	}`)
	out, err := ResponsesToAnthropic(in, "claude-mock")
	if err != nil {
		t.Fatalf("ResponsesToAnthropic: %v", err)
	}
	var got anthropicRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode translated request: %v", err)
	}

	want := "be terse\n\n<skills>a</skills>\n\n<permissions>b</permissions>"
	if got.System != want {
		t.Errorf("system = %q, want %q", got.System, want)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(got.Messages))
	}
	for _, message := range got.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			t.Errorf("message carries role %q, which Anthropic rejects", message.Role)
		}
	}
}
