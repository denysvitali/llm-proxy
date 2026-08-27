package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeCodeRequestToOpenAIChat(t *testing.T) {
	body := []byte(`{
		"model":"claude-code-model","max_tokens":1024,"stream":true,
		"system":[{"type":"text","text":"You are Claude Code."},{"type":"text","text":"Use tools carefully.","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"assistant","content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/a"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YWJj"}}]}
		],
		"tools":[{"name":"Read","description":"Read a file","input_schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","properties":{"file_path":{"type":"string"}},"required":["file_path"]}}],
		"tool_choice":{"type":"tool","name":"Read","disable_parallel_tool_use":true}
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := ToOpenAI(req, "hy3-free")
	if err != nil {
		t.Fatal(err)
	}
	var got openAIRequest
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "hy3-free" || !got.Stream || got.StreamOptionsUsage == nil || !got.StreamOptionsUsage.IncludeUsage {
		t.Fatalf("streaming request fields not preserved: %+v", got)
	}
	if len(got.Messages) != 4 || got.Messages[0].Role != "system" || got.Messages[0].Content != "You are Claude Code.\n\nUse tools carefully." {
		t.Fatalf("translated messages = %#v", got.Messages)
	}
	if len(got.Messages[1].ToolCalls) != 1 || got.Messages[1].ToolCalls[0].Function.Name != "Read" || got.Messages[1].ToolCalls[0].Function.Arguments != `{"file_path":"/tmp/a"}` {
		t.Fatalf("assistant tool call = %#v", got.Messages[1])
	}
	if got.Messages[2].Role != "tool" || got.Messages[2].ToolCallID != "toolu_1" || got.Messages[2].Content != "line one\nline two" {
		t.Fatalf("tool result = %#v", got.Messages[2])
	}
	parts, ok := got.Messages[3].Content.([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("image message content = %#v", got.Messages[3].Content)
	}
	if len(got.Tools) != 1 || bytes.Contains(got.Tools[0].Function.Parameters, []byte(`"$schema"`)) {
		t.Fatalf("tool schema was not normalized: %s", got.Tools[0].Function.Parameters)
	}
	choice, ok := got.ToolChoice.(map[string]any)
	if !ok || choice["type"] != "function" || got.ParallelToolCalls == nil || *got.ParallelToolCalls {
		t.Fatalf("tool choice = %#v, parallel = %v", got.ToolChoice, got.ParallelToolCalls)
	}
}

func TestClaudeCodeStreamingChatResponse(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl_1","model":"hy3-free","choices":[{"delta":{"reasoning_content":"checking"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl_1","model":"hy3-free","choices":[{"delta":{"content":"done"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl_1","model":"hy3-free","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\":"}}]},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl_1","model":"hy3-free","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"/tmp/a\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","model":"hy3-free","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":12}}}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var output bytes.Buffer
	flushes := 0
	stream := NewStreamWriter(&output, func() { flushes++ }, "fallback", true)
	if err := stream.Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		`event: message_start`, `"model":"hy3-free"`, `"type":"thinking_delta"`,
		`"type":"signature_delta"`, `"text":"done","type":"text_delta"`,
		`"id":"toolu_call_1","input":{},"name":"Read","type":"tool_use"`,
		`"partial_json":"{\"file_path\":"`, `"partial_json":"\"/tmp/a\"}"`,
		`"stop_reason":"tool_use"`, `"cache_read_input_tokens":12`, `event: message_stop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stream missing %q:\n%s", want, got)
		}
	}
	if flushes == 0 {
		t.Error("stream never flushed")
	}
}

func TestChatStreamRejectsTruncation(t *testing.T) {
	var output bytes.Buffer
	stream := NewStreamWriter(&output, nil, "model", false)
	err := stream.Consume(strings.NewReader(`data: {"id":"x","choices":[{"delta":{"content":"partial"}}]}`))
	if err == nil || !strings.Contains(err.Error(), "completion marker") {
		t.Fatalf("Consume error = %v", err)
	}
	stream.Fail("upstream disconnected")
	if got := output.String(); !strings.Contains(got, `event: error`) || strings.Contains(got, `event: message_stop`) {
		t.Fatalf("failure stream = %s", got)
	}
}
