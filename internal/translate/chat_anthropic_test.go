package translate

import (
	"bytes"
	"strings"
	"testing"
)

func TestChatToAnthropic(t *testing.T) {
	body := []byte(`{
		"model":"client-model",
		"max_tokens":256,
		"messages":[
			{"role":"system","content":"be nice"},
			{"role":"developer","content":"also terse"},
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"},
			{"role":"user","content":[{"type":"text","text":"thanks"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,QUJD"}}]}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"weather lookup","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
		"tool_choice":{"type":"function","function":{"name":"get_weather"}}
	}`)
	out, err := ChatToAnthropic(body, "upstream-a")
	if err != nil {
		t.Fatalf("ChatToAnthropic: %v", err)
	}
	got := decodeJSON(t, out)

	if got["model"] != "upstream-a" {
		t.Errorf("model = %v, want upstream-a", got["model"])
	}
	if got["max_tokens"].(float64) != 256 {
		t.Errorf("max_tokens = %v, want 256", got["max_tokens"])
	}
	if got["system"] != "be nice\n\nalso terse" {
		t.Errorf("system = %q, want merged prompts", got["system"])
	}

	messages := jarray(t, got["messages"], "messages")
	if len(messages) != 4 {
		t.Fatalf("messages = %d entries, want 4 (system folded out, tool results merged)", len(messages))
	}

	first := jmap(t, messages[0], "")
	firstBlock := jmap(t, jarray(t, first["content"], "content")[0], "")
	if first["role"] != "user" || firstBlock["text"] != "hi" {
		t.Errorf("message 0 = %v, want user text hi", first)
	}

	second := jmap(t, messages[1], "")
	if second["role"] != "assistant" {
		t.Fatalf("message 1 role = %v, want assistant", second["role"])
	}
	blocks := jarray(t, second["content"], "content")
	if len(blocks) != 1 {
		t.Fatalf("assistant blocks = %d, want 1 (empty text dropped)", len(blocks))
	}
	call := jmap(t, blocks[0], "")
	if call["type"] != "tool_use" || call["id"] != "call_1" || call["name"] != "get_weather" {
		t.Errorf("tool_use block = %v", call)
	}
	if city := jmap(t, call["input"], "input")["city"]; city != "Rome" {
		t.Errorf("tool_use input = %v, want decoded arguments", call["input"])
	}

	third := jmap(t, messages[2], "")
	if third["role"] != "user" {
		t.Fatalf("message 2 role = %v, want user", third["role"])
	}
	result := jmap(t, jarray(t, third["content"], "content")[0], "")
	if result["type"] != "tool_result" || result["tool_use_id"] != "call_1" {
		t.Errorf("tool_result block = %v", result)
	}

	fourth := jmap(t, messages[3], "")
	imageBlocks := jarray(t, fourth["content"], "content")
	img := jmap(t, imageBlocks[1], "")
	source := jmap(t, img["source"], "source")
	if img["type"] != "image" || source["type"] != "base64" ||
		source["media_type"] != "image/jpeg" || source["data"] != "QUJD" {
		t.Errorf("image block = %v, want base64 source split from data URL", img)
	}

	tools := jarray(t, got["tools"], "tools")
	tool := jmap(t, tools[0], "")
	if tool["name"] != "get_weather" {
		t.Errorf("tool = %v", tool)
	}
	choice := jmap(t, got["tool_choice"], "tool_choice")
	if choice["type"] != "tool" || choice["name"] != "get_weather" {
		t.Errorf("tool_choice = %v, want forced tool", choice)
	}
}

func TestChatToAnthropicDefaultsMaxTokens(t *testing.T) {
	out, err := ChatToAnthropic([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), "u")
	if err != nil {
		t.Fatalf("ChatToAnthropic: %v", err)
	}
	got := decodeJSON(t, out)
	if got["max_tokens"].(float64) != defaultMaxTokens {
		t.Errorf("max_tokens = %v, want default %d", got["max_tokens"], defaultMaxTokens)
	}
	if _, ok := got["system"]; ok {
		t.Errorf("system = %v, want omitted", got["system"])
	}
}

func TestChatFromAnthropic(t *testing.T) {
	upstream := []byte(`{
		"id":"msg_1",
		"model":"claude-upstream",
		"content":[
			{"type":"thinking","thinking":"pondering"},
			{"type":"text","text":"hello "},
			{"type":"text","text":"world"},
			{"type":"tool_use","id":"toolu_9","name":"lookup","input":{"q":"x"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":12,"output_tokens":30,"cache_read_input_tokens":7}
	}`)
	out, err := ChatFromAnthropic(upstream, "client-m")
	if err != nil {
		t.Fatalf("ChatFromAnthropic: %v", err)
	}
	got := decodeJSON(t, out)

	if got["object"] != "chat.completion" || got["id"] != "msg_1" || got["model"] != "claude-upstream" {
		t.Errorf("envelope = %v", got)
	}
	choices := jarray(t, got["choices"], "choices")
	choice := jmap(t, choices[0], "")
	message := jmap(t, choice["message"], "message")
	if message["content"] != "hello \n\nworld" {
		t.Errorf("content = %q, want text blocks joined", message["content"])
	}
	calls := jarray(t, message["tool_calls"], "tool_calls")
	call := jmap(t, calls[0], "")
	if call["id"] != "toolu_9" || jmap(t, call["function"], "function")["name"] != "lookup" {
		t.Errorf("tool_calls[0] = %v", call)
	}
	if message["reasoning_content"] != "pondering" {
		t.Errorf("reasoning_content = %v", message["reasoning_content"])
	}
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choice["finish_reason"])
	}
	usage := jmap(t, got["usage"], "usage")
	if usage["prompt_tokens"].(float64) != 12 || usage["completion_tokens"].(float64) != 30 {
		t.Errorf("usage = %v", usage)
	}
	details := jmap(t, usage["prompt_tokens_details"], "prompt_tokens_details")
	if details["cached_tokens"].(float64) != 7 {
		t.Errorf("cached_tokens = %v, want 7", details["cached_tokens"])
	}
}

// chatChunkEvents parses chat-completions SSE output; the trailing [DONE]
// sentinel is dropped because it carries no JSON payload.
func chatChunkEvents(t *testing.T, raw string) []sseEvent {
	t.Helper()
	return parseSSE(t, strings.ReplaceAll(raw, "data: [DONE]\n\n", ""))
}

func TestChatStreamFromAnthropic(t *testing.T) {
	upstream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_s","model":"upstream-s","usage":{"input_tokens":9}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hey"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"f"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	var buf bytes.Buffer
	writer := NewChatStreamFromAnthropic(&buf, nil, "client-s")
	if err := writer.Consume(strings.NewReader(upstream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	writer.Finish() // idempotent: second Finish must add nothing

	chunks := chatChunkEvents(t, buf.String())
	if len(chunks) == 0 {
		t.Fatal("no chat chunks emitted")
	}
	first := chunks[0].Data
	if first["id"] != "msg_s" || first["model"] != "upstream-s" {
		t.Errorf("first chunk envelope = %v", first)
	}
	firstChoice := jmap(t, jarray(t, first["choices"], "choices")[0], "")
	firstDelta := jmap(t, firstChoice["delta"], "delta")
	if firstDelta == nil || firstDelta["role"] != "assistant" {
		t.Errorf("first chunk delta = %v, want role assistant", first)
	}

	var sawText, sawToolName, sawArgs bool
	for _, chunk := range chunks {
		choiceAny := jarray(t, chunk.Data["choices"], "choices")[0]
		choiceMap := jmap(t, choiceAny, "")
		if choiceMap["delta"] == nil {
			continue
		}
		dm := choiceMap["delta"].(map[string]any)
		if s, _ := dm["content"].(string); s == "hey" {
			sawText = true
		}
		toolCalls, ok := dm["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, tcAny := range toolCalls {
			fn := jmap(t, tcAny, "")["function"]
			if fn == nil {
				continue
			}
			fm := fn.(map[string]any)
			if fm["name"] == "f" {
				sawToolName = true
			}
			if fm["arguments"] == `{"a":1}` {
				sawArgs = true
			}
		}
	}
	if !sawText || !sawToolName || !sawArgs {
		t.Errorf("stream missing pieces: text=%v toolName=%v args=%v\n%s", sawText, sawToolName, sawArgs, buf.String())
	}

	last := chunks[len(chunks)-1].Data
	finalChoice := jmap(t, jarray(t, last["choices"], "choices")[0], "")
	if finalChoice["finish_reason"] != "tool_calls" {
		t.Errorf("final finish_reason = %v, want tool_calls", finalChoice["finish_reason"])
	}
	usage, ok := last["usage"].(map[string]any)
	if !ok {
		t.Fatalf("final chunk missing usage: %v", last)
	}
	if usage["prompt_tokens"].(float64) != 9 || usage["completion_tokens"].(float64) != 4 {
		t.Errorf("usage = %v, want 9 in / 4 out", usage)
	}
	if !strings.HasSuffix(buf.String(), "data: [DONE]\n\n") {
		t.Errorf("stream must end with [DONE], tail = %q", buf.String()[max(0, len(buf.String())-40):])
	}
}
