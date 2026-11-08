package translate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Open Design's AMR gateway streams sequential tool calls on a single reused
// index: the second call arrives as index 0 again with a new id and name, right
// after the first call's arguments finished. Keying blocks by index alone made
// the translator abort the stream ("changed tool identity at an existing
// index") after the first call had already been forwarded, so clients saw a
// mid-response server error.
func reusedToolIndexStream() string {
	return strings.Join([]string{
		`data: {"id":"resp_1","choices":[{"index":0,"delta":{}}],"model":"m"}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","id":"call_00_a","function":{"name":"bash","arguments":""}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":null,"arguments":"{\"command\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":null,"arguments":"\"echo hi\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","id":"call_01_b","function":{"name":"grep","arguments":""}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":null,"arguments":"{\"pattern\":\"TODO\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`, "",
	}, "\n\n")
}

func TestChatToolIndexReuseStartsANewBlock(t *testing.T) {
	var output bytes.Buffer
	writer := NewStreamWriter(&output, nil, "m", false)
	if err := writer.Consume(strings.NewReader(reusedToolIndexStream())); err != nil {
		t.Fatalf("reused index aborted the stream: %v", err)
	}
	writer.Finish()

	starts := map[int]struct {
		id, name string
	}{}
	arguments := map[int]string{}
	stops := map[int]int{}
	errors := 0
	for _, frame := range lifecycleFrames(t, output.String()) {
		var index int
		if raw := frame["index"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &index); err != nil {
				t.Fatal(err)
			}
		}
		switch frameString(t, frame, "type") {
		case "content_block_start":
			var block struct{ ID, Name, Type string }
			if err := json.Unmarshal(frame["content_block"], &block); err != nil {
				t.Fatal(err)
			}
			if block.Type != "tool_use" {
				continue
			}
			if _, seen := starts[index]; seen {
				t.Fatalf("block %d started twice: %s", index, output.String())
			}
			starts[index] = struct{ id, name string }{block.ID, block.Name}
		case "content_block_delta":
			var delta struct {
				PartialJSON string `json:"partial_json"`
			}
			if err := json.Unmarshal(frame["delta"], &delta); err != nil {
				t.Fatal(err)
			}
			arguments[index] += delta.PartialJSON
		case "content_block_stop":
			stops[index]++
		case "error":
			errors++
		}
	}
	if errors != 0 {
		t.Fatalf("stream failed: %s", output.String())
	}
	want := map[int]struct {
		id, name string
	}{0: {"toolu_call_00_a", "Bash"}, 1: {"toolu_call_01_b", "Grep"}}
	if len(starts) != len(want) {
		t.Fatalf("blocks = %+v", starts)
	}
	for index, block := range want {
		if starts[index] != block {
			t.Fatalf("block %d = %+v, want %+v", index, starts[index], block)
		}
	}
	if arguments[0] != `{"command":"echo hi"}` || arguments[1] != `{"pattern":"TODO"}` {
		t.Fatalf("arguments = %v", arguments)
	}
	for index := range starts {
		if stops[index] != 1 {
			t.Fatalf("block %d stopped %d times", index, stops[index])
		}
	}
}

func TestChatToolIndexReuseResponsesItems(t *testing.T) {
	var output bytes.Buffer
	writer := NewResponsesStreamFromChat(&output, nil, "m")
	if err := writer.Consume(strings.NewReader(reusedToolIndexStream())); err != nil {
		t.Fatalf("reused index aborted the stream: %v", err)
	}
	writer.Finish()

	type item struct{ callID, name, arguments string }
	starts, dones := map[int]item{}, map[int]item{}
	completed := 0
	for _, frame := range lifecycleFrames(t, output.String()) {
		switch frameString(t, frame, "type") {
		case "response.output_item.added", "response.output_item.done":
			var payload struct {
				OutputIndex int `json:"output_index"`
			}
			if err := json.Unmarshal(mustMarshal(frame), &payload); err != nil {
				t.Fatal(err)
			}
			var got responsesOutputItem
			if err := json.Unmarshal(frame["item"], &got); err != nil {
				t.Fatal(err)
			}
			if got.Type != "function_call" {
				continue
			}
			entry := item{got.CallID, got.Name, got.Arguments}
			if frameString(t, frame, "type") == "response.output_item.added" {
				starts[payload.OutputIndex] = entry
			} else {
				dones[payload.OutputIndex] = entry
			}
		case "response.completed":
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("terminal events = %d: %s", completed, output.String())
	}
	want := []item{{"call_00_a", "bash", `{"command":"echo hi"}`}, {"call_01_b", "grep", `{"pattern":"TODO"}`}}
	if len(starts) != len(want) {
		t.Fatalf("items = %+v", starts)
	}
	for index, expected := range want {
		if starts[index] != (item{expected.callID, expected.name, ""}) {
			t.Fatalf("item %d added as %+v", index, starts[index])
		}
		if dones[index] != expected {
			t.Fatalf("item %d done as %+v, want %+v", index, dones[index], expected)
		}
	}
}

// Some upstreams repeat the tool name on every argument delta. For a lowercase
// name the translator canonicalizes it ("bash" -> "Bash"), so comparing the raw
// name against the canonical one made an ordinary continuation look like an
// identity change.
func TestChatToolRepeatedNameOnContinuationIsNotAnIdentityChange(t *testing.T) {
	input := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"bash","arguments":"{\"command\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"bash","arguments":"\"ls\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"bash","arguments":"}"}}]}}]}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var output bytes.Buffer
	writer := NewStreamWriter(&output, nil, "m", false)
	if err := writer.Consume(strings.NewReader(input)); err != nil {
		t.Fatalf("repeated name aborted the stream: %v", err)
	}
	writer.Finish()
	starts := strings.Count(output.String(), `"type":"content_block_start"`)
	stops := strings.Count(output.String(), `"type":"content_block_stop"`)
	if starts != 1 || stops != 1 || !strings.Contains(output.String(), `"partial_json":"}"`) {
		t.Fatalf("blocks started=%d stopped=%d: %s", starts, stops, output.String())
	}
}

// Two calls to the same tool on a reused index announce the same name, so the
// new id is what marks them apart. Merging them would splice two argument
// objects together.
func TestChatToolIndexReuseSameToolTwice(t *testing.T) {
	input := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_2","function":{"name":"bash","arguments":"{\"command\":\"pwd\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var anthropic bytes.Buffer
	if err := NewStreamWriter(&anthropic, nil, "m", false).Consume(strings.NewReader(input)); err != nil {
		t.Fatalf("reused index aborted the stream: %v", err)
	}
	for _, want := range []string{`"id":"toolu_call_1"`, `"id":"toolu_call_2"`, `"partial_json":"{\"command\":\"pwd\"}"`} {
		if !strings.Contains(anthropic.String(), want) {
			t.Fatalf("missing %q: %s", want, anthropic.String())
		}
	}
	var responses bytes.Buffer
	if err := NewResponsesStreamFromChat(&responses, nil, "m").Consume(strings.NewReader(input)); err != nil {
		t.Fatalf("reused index aborted the stream: %v", err)
	}
	for _, want := range []string{`"call_id":"call_1"`, `"call_id":"call_2"`} {
		if !strings.Contains(responses.String(), want) {
			t.Fatalf("missing %q: %s", want, responses.String())
		}
	}
}

// A provider that renumbers a call mid-argument (id changes, no name) is still
// a broken stream: merging it would concatenate two argument objects.
func TestChatToolIDChangeWithoutNameStaysAFailure(t *testing.T) {
	input := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"lookup","arguments":"{\"a\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_b","function":{"arguments":"1}"}}]}}]}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var output bytes.Buffer
	if err := NewStreamWriter(&output, nil, "m", false).Consume(strings.NewReader(input)); err == nil {
		t.Fatalf("identity change accepted: %s", output.String())
	}
	var responses bytes.Buffer
	if err := NewResponsesStreamFromChat(&responses, nil, "m").Consume(strings.NewReader(input)); err == nil {
		t.Fatalf("identity change accepted: %s", responses.String())
	}
}
