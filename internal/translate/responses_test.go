package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers

func decodeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func jmap(t *testing.T, value any, key string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("key %q is not an object: %#v", key, value)
	}
	return object
}

func jarray(t *testing.T, value any, key string) []any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("key %q is not an array: %#v", key, value)
	}
	return array
}

func jstr(t *testing.T, value any) string {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("value is not a string: %#v", value)
	}
	return text
}

type sseEvent struct {
	Name string
	Data map[string]any
}

func parseSSE(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var events []sseEvent
	name := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var data map[string]any
			if err := json.Unmarshal([]byte(payload), &data); err != nil {
				t.Fatalf("decode SSE data %q: %v", payload, err)
			}
			events = append(events, sseEvent{Name: name, Data: data})
			name = ""
		}
	}
	return events
}

func eventsNamed(events []sseEvent, name string) []sseEvent {
	var matched []sseEvent
	for _, event := range events {
		if event.Name == name {
			matched = append(matched, event)
		}
	}
	return matched
}

// ---------------------------------------------------------------------------
// ToResponses

func TestToResponses(t *testing.T) {
	tests := []struct {
		name    string
		request string
		check   func(t *testing.T, out map[string]any)
	}{
		{
			name:    "system becomes instructions",
			request: `{"model":"m","max_tokens":64,"system":"Be terse.","messages":[{"role":"user","content":"Hi"}]}`,
			check: func(t *testing.T, out map[string]any) {
				if got := jstr(t, out["instructions"]); got != "Be terse." {
					t.Fatalf("instructions = %q", got)
				}
			},
		},
		{
			name: "user and assistant text use matching content types",
			request: `{"model":"m","max_tokens":64,"messages":[` +
				`{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi there"},` +
				`{"role":"user","content":"Bye"}]}`,
			check: func(t *testing.T, out map[string]any) {
				input := jarray(t, out["input"], "input")
				if len(input) != 3 {
					t.Fatalf("input has %d items", len(input))
				}
				for i, role := range []string{"user", "assistant", "user"} {
					item := jmap(t, input[i], "item")
					if got := jstr(t, item["role"]); got != role {
						t.Fatalf("item %d role = %q", i, got)
					}
					wantType := "input_text"
					if role == "assistant" {
						wantType = "output_text"
					}
					part := jmap(t, jarray(t, item["content"], "content")[0], "part")
					if got := jstr(t, part["type"]); got != wantType {
						t.Fatalf("item %d content type = %q", i, got)
					}
				}
			},
		},
		{
			name: "assistant tool_use becomes function_call",
			request: `{"model":"m","max_tokens":64,"messages":[` +
				`{"role":"user","content":"Weather?"},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"call_9","name":"get_weather","input":{"city":"SF"}}]}]}`,
			check: func(t *testing.T, out map[string]any) {
				input := jarray(t, out["input"], "input")
				call := jmap(t, input[1], "function_call")
				if got := jstr(t, call["type"]); got != "function_call" {
					t.Fatalf("type = %q", got)
				}
				if got := jstr(t, call["call_id"]); got != "call_9" {
					t.Fatalf("call_id = %q", got)
				}
				if got := jstr(t, call["name"]); got != "get_weather" {
					t.Fatalf("name = %q", got)
				}
				if got := jstr(t, call["arguments"]); got != `{"city":"SF"}` {
					t.Fatalf("arguments = %q", got)
				}
			},
		},
		{
			name: "user tool_result becomes function_call_output",
			request: `{"model":"m","max_tokens":64,"messages":[` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_9","content":"sunny, 18C"}]}]}`,
			check: func(t *testing.T, out map[string]any) {
				output := jmap(t, jarray(t, out["input"], "input")[0], "output")
				if got := jstr(t, output["type"]); got != "function_call_output" {
					t.Fatalf("type = %q", got)
				}
				if got := jstr(t, output["call_id"]); got != "call_9" {
					t.Fatalf("call_id = %q", got)
				}
				if got := jstr(t, output["output"]); got != "sunny, 18C" {
					t.Fatalf("output = %q", got)
				}
			},
		},
		{
			name: "tools are flattened to Responses function tools",
			request: `{"model":"m","max_tokens":64,"tools":[{"name":"get_weather","description":"Look up weather",` +
				`"input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],` +
				`"messages":[{"role":"user","content":"Weather?"}]}`,
			check: func(t *testing.T, out map[string]any) {
				tools := jarray(t, out["tools"], "tools")
				if len(tools) != 1 {
					t.Fatalf("tools has %d entries", len(tools))
				}
				tool := jmap(t, tools[0], "tool")
				if got := jstr(t, tool["type"]); got != "function" {
					t.Fatalf("type = %q", got)
				}
				if got := jstr(t, tool["name"]); got != "get_weather" {
					t.Fatalf("name = %q", got)
				}
				schema := jmap(t, tool["parameters"], "parameters")
				if got := jstr(t, schema["type"]); got != "object" {
					t.Fatalf("schema type = %q", got)
				}
			},
		},
		{
			name: "thinking blocks are dropped from history",
			request: `{"model":"m","max_tokens":64,"messages":[` +
				`{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"sig"},` +
				`{"type":"text","text":"Answer"}]},` +
				`{"role":"user","content":"next"}]}`,
			check: func(t *testing.T, out map[string]any) {
				encoded, err := json.Marshal(out["input"])
				if err != nil {
					t.Fatalf("marshal input: %v", err)
				}
				if strings.Contains(string(encoded), "thinking") {
					t.Fatalf("thinking leaked into input: %s", encoded)
				}
			},
		},
		{
			name:    "limits and sampling fields pass through",
			request: `{"model":"ignored","max_tokens":256,"temperature":0.4,"top_p":0.9,"stream":true,"messages":[{"role":"user","content":"Go"}]}`,
			check: func(t *testing.T, out map[string]any) {
				if got := out["max_output_tokens"].(float64); int(got) != 256 {
					t.Fatalf("max_output_tokens = %v", got)
				}
				if got := out["temperature"].(float64); got != 0.4 {
					t.Fatalf("temperature = %v", got)
				}
				if got := out["top_p"].(float64); got != 0.9 {
					t.Fatalf("top_p = %v", got)
				}
				if out["stream"] != true {
					t.Fatalf("stream = %v", out["stream"])
				}
				if got := jstr(t, out["model"]); got != "target-model" {
					t.Fatalf("model = %q", got)
				}
			},
		},
		{
			name: "tool_choice mapping",
			request: `{"model":"m","max_tokens":64,"tools":[{"name":"f","input_schema":{"type":"object"}}],` +
				`"tool_choice":{"type":"any"},"messages":[{"role":"user","content":"x"}]}`,
			check: func(t *testing.T, out map[string]any) {
				if got := jstr(t, out["tool_choice"]); got != "required" {
					t.Fatalf("tool_choice = %q", got)
				}
			},
		},
		{
			name: "forced tool names at top level",
			request: `{"model":"m","max_tokens":64,"tools":[{"name":"f","input_schema":{"type":"object"}}],` +
				`"tool_choice":{"type":"tool","name":"f"},"messages":[{"role":"user","content":"x"}]}`,
			check: func(t *testing.T, out map[string]any) {
				choice := jmap(t, out["tool_choice"], "tool_choice")
				if got := jstr(t, choice["type"]); got != "function" {
					t.Fatalf("type = %q", got)
				}
				if got := jstr(t, choice["name"]); got != "f" {
					t.Fatalf("name = %q", got)
				}
			},
		},
		{
			name: "thinking request enables reasoning",
			request: `{"model":"m","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":1024},` +
				`"messages":[{"role":"user","content":"Puzzle"}]}`,
			check: func(t *testing.T, out map[string]any) {
				reasoning := jmap(t, out["reasoning"], "reasoning")
				if got := jstr(t, reasoning["effort"]); got != "high" {
					t.Fatalf("effort = %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := ParseRequest([]byte(tt.request))
			if err != nil {
				t.Fatalf("ParseRequest: %v", err)
			}
			body, err := ToResponses(request, "target-model")
			if err != nil {
				t.Fatalf("ToResponses: %v", err)
			}
			tt.check(t, decodeJSON(t, body))
		})
	}
}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// FromResponses

func TestFromResponses(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		includeThinking bool
		check           func(t *testing.T, out map[string]any)
	}{
		{
			name: "text only response",
			body: `{"id":"resp_1","status":"completed",` +
				`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello!"}]}],` +
				`"usage":{"input_tokens":12,"output_tokens":7}}`,
			check: func(t *testing.T, out map[string]any) {
				if got := jstr(t, out["type"]); got != "message" {
					t.Fatalf("type = %q", got)
				}
				content := jarray(t, out["content"], "content")
				if len(content) != 1 {
					t.Fatalf("content has %d blocks", len(content))
				}
				block := jmap(t, content[0], "block")
				if got := jstr(t, block["type"]); got != "text" {
					t.Fatalf("block type = %q", got)
				}
				if got := jstr(t, block["text"]); got != "Hello!" {
					t.Fatalf("text = %q", got)
				}
				if got := jstr(t, out["stop_reason"]); got != "end_turn" {
					t.Fatalf("stop_reason = %#v", out["stop_reason"])
				}
			},
		},
		{
			name: "function call maps to tool_use and usage transfers",
			body: `{"id":"resp_2","status":"completed",` +
				`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checking."}]},` +
				`{"type":"function_call","call_id":"call_5","name":"get_weather","arguments":"{\"city\":\"SF\"}"}],` +
				`"usage":{"input_tokens":20,"output_tokens":9}}`,
			check: func(t *testing.T, out map[string]any) {
				usage := jmap(t, out["usage"], "usage")
				if got := int(usage["input_tokens"].(float64)); got != 20 {
					t.Fatalf("input_tokens = %d", got)
				}
				if got := int(usage["output_tokens"].(float64)); got != 9 {
					t.Fatalf("output_tokens = %d", got)
				}
				content := jarray(t, out["content"], "content")
				tool := jmap(t, content[1], "tool_use")
				if got := jstr(t, tool["type"]); got != "tool_use" {
					t.Fatalf("block type = %q", got)
				}
				if got := jstr(t, tool["id"]); got != "call_5" {
					t.Fatalf("id = %q", got)
				}
				if got := jstr(t, tool["name"]); got != "get_weather" {
					t.Fatalf("name = %q", got)
				}
				if got := jmap(t, tool["input"], "input")["city"]; got != "SF" {
					t.Fatalf("input = %#v", tool["input"])
				}
				if got := out["stop_reason"]; got != "tool_use" {
					t.Fatalf("stop_reason = %#v", got)
				}
			},
		},
		{
			name: "incomplete status means max_tokens",
			body: `{"id":"resp_3","status":"incomplete",` +
				`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],` +
				`"usage":{"input_tokens":1,"output_tokens":100}}`,
			check: func(t *testing.T, out map[string]any) {
				if got := out["stop_reason"]; got != "max_tokens" {
					t.Fatalf("stop_reason = %#v", got)
				}
			},
		},
		{
			name: "reasoning suppressed by default",
			body: `{"id":"resp_4","status":"completed",` +
				`"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"secret plan"}]},` +
				`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"42"}]}],` +
				`"usage":{"input_tokens":3,"output_tokens":4}}`,
			check: func(t *testing.T, out map[string]any) {
				content := jarray(t, out["content"], "content")
				if len(content) != 1 || jstr(t, jmap(t, content[0], "b")["type"]) != "text" {
					t.Fatalf("content should hold one text block: %#v", content)
				}
			},
		},
		{
			name:            "reasoning surfaced when requested",
			includeThinking: true,
			body: `{"id":"resp_5","status":"completed",` +
				`"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"secret plan"}]},` +
				`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"42"}]}],` +
				`"usage":{"input_tokens":3,"output_tokens":4}}`,
			check: func(t *testing.T, out map[string]any) {
				content := jarray(t, out["content"], "content")
				thinking := jmap(t, content[0], "thinking")
				if got := jstr(t, thinking["type"]); got != "thinking" {
					t.Fatalf("first block = %q", got)
				}
				if got := jstr(t, thinking["thinking"]); got != "secret plan" {
					t.Fatalf("thinking = %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := FromResponses([]byte(tt.body), "client-model", tt.includeThinking)
			if err != nil {
				t.Fatalf("FromResponses: %v", err)
			}
			decoded := decodeJSON(t, out)
			if model := decoded["model"]; model != "client-model" {
				t.Fatalf("model = %#v, want client-model override kept unless upstream set", model)
			}
			tt.check(t, decoded)
		})
	}
}

// TestFromResponsesUpstreamModelWins verifies an upstream model label is
// passed through like FromOpenAI does.
func TestFromResponsesUpstreamModelWins(t *testing.T) {
	out, err := FromResponses([]byte(
		`{"id":"resp_6","status":"completed","model":"up-model",`+
			`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],`+
			`"usage":{}}`), "client-model", false)
	if err != nil {
		t.Fatalf("FromResponses: %v", err)
	}
	if got := jstr(t, decodeJSON(t, out)["model"]); got != "up-model" {
		t.Fatalf("model = %q", got)
	}
}

// TestResponsesRoundTrip checks that a request carrying a tool definition and
// history survives conversion in both directions.
func TestResponsesRoundTrip(t *testing.T) {
	anthropicRequest := `{"model":"claude-ish","max_tokens":512,"system":"Use tools.","` +
		`tools":[{"name":"get_weather","description":"weather lookup","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],` +
		`"messages":[{"role":"user","content":"Weather in SF?"}]}`
	request, err := ParseRequest([]byte(anthropicRequest))
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	responsesBody, err := ToResponses(request, "target")
	if err != nil {
		t.Fatalf("ToResponses: %v", err)
	}
	converted := decodeJSON(t, responsesBody)
	if got := jstr(t, converted["instructions"]); got != "Use tools." {
		t.Fatalf("instructions = %q", got)
	}

	upstream := `{"id":"resp_rt","status":"completed","model":"target",` +
		`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Let me check."}]},` +
		`{"type":"function_call","call_id":"call_rt","name":"get_weather","arguments":"{\"city\":\"SF\"}"}],` +
		`"usage":{"input_tokens":15,"output_tokens":8}}`
	anthropicBody, err := FromResponses([]byte(upstream), "claude-ish", false)
	if err != nil {
		t.Fatalf("FromResponses: %v", err)
	}
	response := decodeJSON(t, anthropicBody)
	content := jarray(t, response["content"], "content")
	if len(content) != 2 {
		t.Fatalf("content has %d blocks", len(content))
	}
	text := jmap(t, content[0], "text")
	tool := jmap(t, content[1], "tool")
	if jstr(t, text["text"]) != "Let me check." || jstr(t, tool["name"]) != "get_weather" {
		t.Fatalf("round trip mismatch: %#v %#v", text, tool)
	}

	// Feed the tool result back through ToResponses; the function_call_output
	// must reference the same call id the model produced.
	followUp, err := ParseRequest([]byte(`{"model":"claude-ish","max_tokens":512,"messages":[` +
		`{"role":"user","content":"Weather in SF?"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"call_rt","name":"get_weather","input":{"city":"SF"}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_rt","content":"foggy, 14C"}]}]}`))
	if err != nil {
		t.Fatalf("ParseRequest follow-up: %v", err)
	}
	followUpBody, err := ToResponses(followUp, "target")
	if err != nil {
		t.Fatalf("ToResponses follow-up: %v", err)
	}
	followUpConverted := decodeJSON(t, followUpBody)
	items := jarray(t, followUpConverted["input"], "input")
	callItem := jmap(t, items[1], "call")
	outputItem := jmap(t, items[2], "output")
	if jstr(t, callItem["call_id"]) != "call_rt" || jstr(t, outputItem["call_id"]) != "call_rt" {
		t.Fatalf("call ids diverged: %#v %#v", callItem, outputItem)
	}
}

// ---------------------------------------------------------------------------
// ResponsesStreamWriter

const cannedResponsesStream = `data: {"type":"response.created","response":{"id":"resp_s1","model":"stream-model"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant"}}

data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}

data: {"type":"response.output_text.delta","output_index":0,"delta":" world"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant"}}

data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_s1","name":"get_weather"}}

data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":"}

data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"SF\"}"}

data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call"}}

data: {"type":"response.completed","response":{"id":"resp_s1","status":"completed","usage":{"input_tokens":10,"output_tokens":5}}}

`

func TestResponsesStreamWriter(t *testing.T) {
	var buffer bytes.Buffer
	flushes := 0
	writer := NewResponsesStreamWriter(&buffer, func() { flushes++ }, "client-model", false)
	if err := writer.Consume(strings.NewReader(cannedResponsesStream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	writer.Finish()

	events := parseSSE(t, buffer.String())
	if len(events) == 0 {
		t.Fatal("no events emitted")
	}
	if first := events[0].Name; first != "message_start" {
		t.Fatalf("first event = %q, want message_start", first)
	}
	if last := events[len(events)-1].Name; last != "message_stop" {
		t.Fatalf("last event = %q, want message_stop", last)
	}
	startMessage := jmap(t, events[0].Data["message"], "message")
	if got := jstr(t, startMessage["model"]); got != "stream-model" {
		t.Fatalf("message_start model = %q", got)
	}

	// Text deltas land inside a single text block.
	var deltas []string
	var jsonParts []string
	toolStarted := false
	textBlockIndex := -1
	toolBlockIndex := -1
	blockStops := 0
	for _, event := range events {
		switch event.Name {
		case "content_block_start":
			block := jmap(t, event.Data["content_block"], "block")
			switch jstr(t, block["type"]) {
			case "text":
				textBlockIndex = int(event.Data["index"].(float64))
			case "tool_use":
				toolStarted = true
				toolBlockIndex = int(event.Data["index"].(float64))
				if got := jstr(t, block["name"]); got != "get_weather" {
					t.Fatalf("tool name = %q", got)
				}
				if got := jstr(t, block["id"]); got != "call_s1" {
					t.Fatalf("tool id = %q", got)
				}
			}
		case "content_block_delta":
			delta := jmap(t, event.Data["delta"], "delta")
			switch jstr(t, delta["type"]) {
			case "text_delta":
				deltas = append(deltas, jstr(t, delta["text"]))
			case "input_json_delta":
				jsonParts = append(jsonParts, jstr(t, delta["partial_json"]))
			}
		case "content_block_stop":
			blockStops++
		}
	}
	if strings.Join(deltas, "") != "Hello world" {
		t.Fatalf("text deltas = %#v", deltas)
	}
	if !toolStarted {
		t.Fatal("no tool_use content_block_start emitted")
	}
	if textBlockIndex == toolBlockIndex {
		t.Fatalf("text and tool blocks share index %d", textBlockIndex)
	}
	if got := strings.Join(jsonParts, ""); got != `{"city":"SF"}` {
		t.Fatalf("input_json_delta accumulated to %q", got)
	}
	if blockStops != 2 {
		t.Fatalf("got %d content_block_stop events, want 2", blockStops)
	}

	messageDeltas := eventsNamed(events, "message_delta")
	if len(messageDeltas) != 1 {
		t.Fatalf("got %d message_delta events", len(messageDeltas))
	}
	delta := jmap(t, messageDeltas[0].Data["delta"], "delta")
	if got := jstr(t, delta["stop_reason"]); got != "tool_use" {
		t.Fatalf("stop_reason = %q", got)
	}
	usage := jmap(t, messageDeltas[0].Data["usage"], "usage")
	if got := int(usage["input_tokens"].(float64)); got != 10 {
		t.Fatalf("usage input_tokens = %d", got)
	}
	if got := int(usage["output_tokens"].(float64)); got != 5 {
		t.Fatalf("usage output_tokens = %d", got)
	}
	if flushes == 0 {
		t.Fatal("flush was never called")
	}
}

func TestResponsesStreamWriterThinking(t *testing.T) {
	stream := `data: {"type":"response.created","response":{"id":"resp_t1","model":"m"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning"}}

data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"pondering"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning"}}

data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","role":"assistant"}}

data: {"type":"response.output_text.delta","output_index":1,"delta":"answer"}

data: {"type":"response.completed","response":{"id":"resp_t1","status":"completed","usage":{}}}

`

	t.Run("suppressed", func(t *testing.T) {
		var buffer bytes.Buffer
		writer := NewResponsesStreamWriter(&buffer, nil, "m", false)
		if err := writer.Consume(strings.NewReader(stream)); err != nil {
			t.Fatalf("Consume: %v", err)
		}
		for _, event := range parseSSE(t, buffer.String()) {
			if event.Name == "content_block_start" {
				block := jmap(t, event.Data["content_block"], "block")
				if got := jstr(t, block["type"]); got == "thinking" {
					t.Fatal("thinking block emitted despite includeThinking=false")
				}
			}
			if event.Name == "content_block_delta" {
				delta := jmap(t, event.Data["delta"], "delta")
				if jstr(t, delta["type"]) == "thinking_delta" {
					t.Fatal("thinking delta leaked")
				}
			}
		}
	})

	t.Run("included", func(t *testing.T) {
		var buffer bytes.Buffer
		writer := NewResponsesStreamWriter(&buffer, nil, "m", true)
		if err := writer.Consume(strings.NewReader(stream)); err != nil {
			t.Fatalf("Consume: %v", err)
		}
		var sawThinking, sawSignature bool
		for _, event := range parseSSE(t, buffer.String()) {
			if event.Name != "content_block_delta" {
				continue
			}
			delta := jmap(t, event.Data["delta"], "delta")
			switch jstr(t, delta["type"]) {
			case "thinking_delta":
				if jstr(t, delta["thinking"]) != "pondering" {
					t.Fatalf("thinking delta = %q", delta["thinking"])
				}
				sawThinking = true
			case "signature_delta":
				sawSignature = true
			}
		}
		if !sawThinking || !sawSignature {
			t.Fatalf("thinking=%v signature=%v", sawThinking, sawSignature)
		}
	})
}

// TestResponsesStreamWriterFinishWithoutCompleted feeds a stream that ends
// without response.completed; Finish must still close the message.
func TestResponsesStreamWriterFinishWithoutCompleted(t *testing.T) {
	stream := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant"}}

data: {"type":"response.output_text.delta","output_index":0,"delta":"cut short"}

`
	var buffer bytes.Buffer
	writer := NewResponsesStreamWriter(&buffer, nil, "m", false)
	if err := writer.Consume(strings.NewReader(stream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	writer.Finish()

	events := parseSSE(t, buffer.String())
	if last := events[len(events)-1].Name; last != "message_stop" {
		t.Fatalf("last event = %q, want message_stop", last)
	}
	messageDeltas := eventsNamed(events, "message_delta")
	if len(messageDeltas) != 1 {
		t.Fatalf("got %d message_delta events", len(messageDeltas))
	}
	delta := jmap(t, messageDeltas[0].Data["delta"], "delta")
	if got := jstr(t, delta["stop_reason"]); got != "end_turn" {
		t.Fatalf("stop_reason = %q, want default end_turn", got)
	}
}

// ---------------------------------------------------------------------------
// Chat <-> Responses

func TestChatToResponses(t *testing.T) {
	chatBody := `{
		"model":"chat-model",
		"max_completion_tokens":512,
		"stream":true,
		"messages":[
			{"role":"system","content":"Be terse."},
			{"role":"user","content":"Weather?"},
			{"role":"assistant","content":"Checking.","tool_calls":[
				{"id":"call_c1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_c1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"weather","parameters":{"type":"object"}}}],
		"tool_choice":"required"
	}`
	body, err := ChatToResponses([]byte(chatBody), "responses-model")
	if err != nil {
		t.Fatalf("ChatToResponses: %v", err)
	}
	out := decodeJSON(t, body)

	if got := jstr(t, out["instructions"]); got != "Be terse." {
		t.Fatalf("instructions = %q", got)
	}
	if got := jstr(t, out["model"]); got != "responses-model" {
		t.Fatalf("model = %q", got)
	}
	if out["stream"] != true {
		t.Fatalf("stream = %v", out["stream"])
	}
	if got := int(out["max_output_tokens"].(float64)); got != 512 {
		t.Fatalf("max_output_tokens = %d", got)
	}
	if got := jstr(t, out["tool_choice"]); got != "required" {
		t.Fatalf("tool_choice = %q", got)
	}

	items := jarray(t, out["input"], "input")
	if len(items) != 4 {
		t.Fatalf("input has %d items: %#v", len(items), items)
	}
	user := jmap(t, items[0], "user")
	if jstr(t, user["role"]) != "user" {
		t.Fatalf("item 0 = %#v", user)
	}
	assistantCall := jmap(t, items[2], "assistantCall")
	if jstr(t, assistantCall["type"]) != "function_call" || jstr(t, assistantCall["call_id"]) != "call_c1" {
		t.Fatalf("assistant tool_calls not converted: %#v", assistantCall)
	}
	result := jmap(t, items[3], "result")
	if jstr(t, result["type"]) != "function_call_output" || jstr(t, result["output"]) != "sunny" {
		t.Fatalf("tool message not converted: %#v", result)
	}

	tools := jarray(t, out["tools"], "tools")
	tool := jmap(t, tools[0], "tool")
	if jstr(t, tool["type"]) != "function" || jstr(t, tool["name"]) != "get_weather" {
		t.Fatalf("tool = %#v", tool)
	}
}

// TestChatToResponsesForcedTool covers the nested chat tool_choice form.
func TestChatToResponsesForcedTool(t *testing.T) {
	chatBody := `{"messages":[{"role":"user","content":"hi"}],` +
		`"tool_choice":{"type":"function","function":{"name":"pick"}}}`
	body, err := ChatToResponses([]byte(chatBody), "m")
	if err != nil {
		t.Fatalf("ChatToResponses: %v", err)
	}
	choice := jmap(t, decodeJSON(t, body)["tool_choice"], "tool_choice")
	if jstr(t, choice["type"]) != "function" || jstr(t, choice["name"]) != "pick" {
		t.Fatalf("tool_choice = %#v", choice)
	}
}

func TestChatFromResponses(t *testing.T) {
	upstream := `{"id":"resp_c1","status":"completed","model":"responses-model",` +
		`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Calling."}]},` +
		`{"type":"function_call","call_id":"call_c1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}],` +
		`"usage":{"input_tokens":11,"output_tokens":6,"total_tokens":17}}`
	body, err := ChatFromResponses([]byte(upstream), "chat-model")
	if err != nil {
		t.Fatalf("ChatFromResponses: %v", err)
	}
	out := decodeJSON(t, body)

	if got := jstr(t, out["object"]); got != "chat.completion" {
		t.Fatalf("object = %q", got)
	}
	choices := jarray(t, out["choices"], "choices")
	choice := jmap(t, choices[0], "choice")
	message := jmap(t, choice["message"], "message")
	if got := jstr(t, message["role"]); got != "assistant" {
		t.Fatalf("role = %q", got)
	}
	if got := jstr(t, message["content"]); got != "Calling." {
		t.Fatalf("content = %q", got)
	}
	calls := jarray(t, message["tool_calls"], "tool_calls")
	call := jmap(t, calls[0], "call")
	if jstr(t, call["id"]) != "call_c1" || jstr(t, jmap(t, call["function"], "function")["name"]) != "get_weather" {
		t.Fatalf("tool_calls = %#v", call)
	}
	if got := jstr(t, choice["finish_reason"]); got != "tool_calls" {
		t.Fatalf("finish_reason = %q", got)
	}
	usage := jmap(t, out["usage"], "usage")
	if int(usage["prompt_tokens"].(float64)) != 11 || int(usage["completion_tokens"].(float64)) != 6 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestChatStreamFromResponses(t *testing.T) {
	stream := `data: {"type":"response.created","response":{"id":"resp_cs","model":"up"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant"}}

data: {"type":"response.output_text.delta","output_index":0,"delta":"Hi"}

data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_cs","name":"get_weather"}}

data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":"}

data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"SF\"}"}

data: {"type":"response.completed","response":{"id":"resp_cs","status":"completed","usage":{"input_tokens":7,"output_tokens":3}}}

`
	var buffer bytes.Buffer
	writer := ChatStreamFromResponses(&buffer, nil, "chat-model")
	if err := writer.Consume(strings.NewReader(stream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	writer.Finish()

	raw := buffer.String()
	if !strings.HasSuffix(raw, "data: [DONE]\n\n") {
		t.Fatalf("stream must end with [DONE], got %q", raw[max(0, len(raw)-40):])
	}

	type chunk struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int `json:"index"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}

	var content strings.Builder
	var arguments strings.Builder
	var sawRoleChunk, sawToolName bool
	var finishReason string
	var usagePrompt int64
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var chunk chunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("decode chunk %q: %v", payload, err)
		}
		if chunk.Object != "chat.completion.chunk" {
			t.Fatalf("object = %q", chunk.Object)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Role != "" {
				sawRoleChunk = true
			}
			content.WriteString(choice.Delta.Content)
			for _, toolCall := range choice.Delta.ToolCalls {
				if toolCall.Function.Name != "" {
					sawToolName = true
				}
				arguments.WriteString(toolCall.Function.Arguments)
			}
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}
		}
		if chunk.Usage != nil {
			usagePrompt = chunk.Usage.PromptTokens
		}
	}
	if !sawRoleChunk {
		t.Fatal("no leading role chunk")
	}
	if content.String() != "Hi" {
		t.Fatalf("content chunks = %q", content.String())
	}
	if !sawToolName {
		t.Fatal("tool name never announced")
	}
	if arguments.String() != `{"city":"SF"}` {
		t.Fatalf("arguments = %q", arguments.String())
	}
	if finishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q", finishReason)
	}
	if usagePrompt != 7 {
		t.Fatalf("usage prompt_tokens = %d", usagePrompt)
	}
}

// TestChatStreamFromResponsesFinishWithoutCompleted ends the stream early;
// the writer must still terminate the client stream.
func TestChatStreamFromResponsesFinishWithoutCompleted(t *testing.T) {
	stream := "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\n"
	var buffer bytes.Buffer
	writer := ChatStreamFromResponses(&buffer, nil, "chat-model")
	if err := writer.Consume(strings.NewReader(stream)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	writer.Finish()
	raw := buffer.String()
	if !strings.Contains(raw, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish chunk: %q", raw)
	}
	if !strings.HasSuffix(raw, "data: [DONE]\n\n") {
		t.Fatal("missing [DONE]")
	}
}
