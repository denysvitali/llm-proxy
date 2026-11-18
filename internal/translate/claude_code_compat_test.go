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
		"system":[{"type":"text","text":"You are Claude Code."},{"type":"text","text":"Use tools carefully."}],
		"messages":[
			{"role":"assistant","content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/a"}}]},
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
	if len(got.Messages) != 4 || got.Messages[0].Role != "system" {
		t.Fatalf("translated messages = %#v", got.Messages)
	}
	assertContentBlocks(t, got.Messages[0].Content.([]any), []map[string]any{{"type": "text", "text": "You are Claude Code."}, {"type": "text", "text": "Use tools carefully."}})
	if len(got.Messages[1].ToolCalls) != 1 || got.Messages[1].ToolCalls[0].Function.Name != "Read" || got.Messages[1].ToolCalls[0].Function.Arguments != `{"file_path":"/tmp/a"}` {
		t.Fatalf("assistant tool call = %#v", got.Messages[1])
	}
	if got.Messages[2].Role != "tool" || got.Messages[2].ToolCallID != "toolu_1" {
		t.Fatalf("tool result = %#v", got.Messages[2])
	}
	assertContentBlocks(t, got.Messages[2].Content.([]any), []map[string]any{{"type": "text", "text": "line one"}, {"type": "text", "text": "line two"}})
	parts, ok := got.Messages[3].Content.([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("image message content = %#v", got.Messages[3].Content)
	}
	if len(got.Tools) != 1 || !bytes.Equal(got.Tools[0].Function.Parameters, req.Tools[0].InputSchema) {
		t.Fatalf("tool schema was not preserved: %s", got.Tools[0].Function.Parameters)
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
	stream := NewStreamWriter(&output, func() { flushes++ }, "fallback", false)
	if err := stream.Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		`event: message_start`, `"model":"hy3-free"`, `"text":"done","type":"text_delta"`,
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
	err := stream.Consume(strings.NewReader(`data: {"id":"x","choices":[{"delta":{"content":"partial"}}]}` + "\n\n"))
	if err == nil || !strings.Contains(err.Error(), "completion marker") {
		t.Fatalf("Consume error = %v", err)
	}
	stream.Fail("upstream disconnected")
	if got := output.String(); !strings.Contains(got, `event: error`) || strings.Contains(got, `event: message_stop`) {
		t.Fatalf("failure stream = %s", got)
	}
}

func TestClaudeCodeControlsToOpenAIChat(t *testing.T) {
	for effort, want := range map[string]string{"low": "low", "medium": "medium", "high": "high", "max": "high"} {
		req, err := ParseRequest([]byte(`{"model":"m","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":1024},` +
			`"output_config":{"effort":"` + effort + `"},"metadata":{"user_id":"u"},"context_management":{"edits":[]},` +
			`"messages":[{"role":"user","content":"hi"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := ToOpenAI(req, "model")
		if err != nil {
			t.Fatalf("effort %s: %v", effort, err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if string(got["reasoning_effort"]) != `"`+want+`"` {
			t.Fatalf("effort %s: reasoning_effort = %s", effort, got["reasoning_effort"])
		}
		for _, key := range []string{"metadata", "context_management", "output_config", "thinking"} {
			if _, ok := got[key]; ok {
				t.Fatalf("%s forwarded: %s", key, encoded)
			}
		}
	}
}

// Claude Code marks cache breakpoints on system, message, and tool blocks,
// replays signed thinking, and offers Anthropic server tools; all of these
// must degrade rather than fail the turn on OpenAI routes.
func TestClaudeCodeAnnotationsDegrade(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,
		"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"sig"},{"type":"redacted_thinking","data":"x"},{"type":"text","text":"ok","citations":null},{"type":"tool_use","id":"toolu_1","name":"Read","input":{},"cache_control":{"type":"ephemeral"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"data","cache_control":{"type":"ephemeral"}}],"cache_control":{"type":"ephemeral"}}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"only","signature":"sig"}]},
			{"role":"user","content":"next"}
		],
		"tools":[
			{"name":"Read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"},"eager_input_streaming":true},
			{"type":"web_search_20250305","name":"web_search","max_uses":5}
		]}`)
	for _, convert := range []func(*Request, string) ([]byte, error){ToOpenAI, ToResponses} {
		req, err := ParseRequest(body)
		if err != nil {
			t.Fatal(err)
		}
		out, err := convert(req, "model")
		if err != nil {
			t.Fatal(err)
		}
		for _, leaked := range []string{"cache_control", "thinking", "signature", "web_search", "eager_input_streaming", "citations"} {
			if strings.Contains(string(out), leaked) {
				t.Fatalf("%s leaked: %s", leaked, out)
			}
		}
		if !strings.Contains(string(out), `"Read"`) || !strings.Contains(string(out), `"next"`) {
			t.Fatalf("content lost: %s", out)
		}
	}
}

// Claude Code 2.1.3xx carries hook context as system messages inside messages.
func TestClaudeCodeMidConversationSystem(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,"messages":[
		{"role":"user","content":"hi"},
		{"role":"system","content":[{"type":"text","text":"hook context","cache_control":{"type":"ephemeral"}}]},
		{"role":"system","content":"more context"}]}`)
	for _, convert := range []func(*Request, string) ([]byte, error){ToOpenAI, ToResponses} {
		req, err := ParseRequest(body)
		if err != nil {
			t.Fatal(err)
		}
		out, err := convert(req, "model")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(out), `"role":"system"`) != 2 || !strings.Contains(string(out), "hook context") {
			t.Fatalf("system messages not carried: %s", out)
		}
	}
	req, err := ParseRequest([]byte(`{"model":"m","max_tokens":64,"messages":[{"role":"system","content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/a.png"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := ToOpenAI(req, "model"); err == nil {
		t.Fatalf("non-text system message accepted: %s", out)
	}
}

// The ChatGPT Codex endpoint labels message items with a phase.
func TestCodexPhaseLabelDropped(t *testing.T) {
	body := []byte(`{"id":"resp_1","status":"completed","model":"gpt","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"pong","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	out, err := FromResponses(body, "m", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"text":"pong"`) || strings.Contains(string(out), "phase") {
		t.Fatalf("FromResponses = %s", out)
	}
	if _, err := ChatFromResponses(body, "m"); err != nil {
		t.Fatal(err)
	}
}
