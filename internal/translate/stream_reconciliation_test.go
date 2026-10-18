package translate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func streamFixture(events ...string) string {
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

func streamedToolArguments(t *testing.T, destination, output string) map[string]string {
	t.Helper()
	arguments := map[string]string{}
	ids := map[int]string{}
	for _, frame := range lifecycleFrames(t, output) {
		switch destination {
		case "chat":
			var chunk chatChunk
			if err := json.Unmarshal(mustMarshal(frame), &chunk); err != nil {
				t.Fatal(err)
			}
			for _, choice := range chunk.Choices {
				for _, call := range choice.Delta.ToolCalls {
					if call.ID != "" {
						ids[call.Index] = call.ID
					}
					arguments[ids[call.Index]] += call.Function.Arguments
				}
			}
		case "messages":
			var event anthropicEvent
			if err := json.Unmarshal(mustMarshal(frame), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "content_block_start" && event.ContentBlock.Type == "tool_use" {
				ids[event.Index] = event.ContentBlock.ID
			}
			if event.Type == "content_block_delta" && event.Delta.Type == "input_json_delta" {
				arguments[ids[event.Index]] += event.Delta.PartialJSON
			}
		case "responses":
			var event responsesEvent
			if err := json.Unmarshal(mustMarshal(frame), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "response.output_item.added" && event.Item.Type == "function_call" {
				ids[event.OutputIndex] = event.Item.CallID
			}
			if event.Type == "response.function_call_arguments.delta" {
				arguments[ids[event.OutputIndex]] += event.Delta
			}
		}
	}
	return arguments
}

func TestAnthropicIndexedStreamLifecycle(t *testing.T) {
	input := streamFixture(
		`{"type":"message_start","message":{"id":"msg_local","model":"upstream"}}`,
		`{"type":"content_block_start","index":7,"content_block":{"type":"tool_use","id":"a","name":"alpha","input":{}}}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"b","name":"beta","input":{ "b": 2 }}}`,
		`{"type":"content_block_start","index":7,"content_block":{"type":"tool_use","id":"a","name":"alpha","input":{}}}`,
		`{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"9007199254740993}"}}`,
		`{"type":"content_block_stop","index":7}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`,
	)
	for _, destination := range []string{"chat", "responses"} {
		t.Run(destination, func(t *testing.T) {
			var output bytes.Buffer
			var err error
			if destination == "chat" {
				err = NewChatStreamFromAnthropic(&output, nil, "fallback").Consume(strings.NewReader(input))
			} else {
				err = NewResponsesStreamFromAnthropic(&output, nil, "fallback").Consume(strings.NewReader(input))
			}
			if err != nil {
				t.Fatal(err)
			}
			got := streamedToolArguments(t, destination, output.String())
			if !reflect.DeepEqual(got, map[string]string{"a": `{"a":9007199254740993}`, "b": `{ "b": 2 }`}) {
				t.Fatalf("arguments=%v", got)
			}
			if destination == "responses" {
				starts, stops := map[string]int{}, map[string]int{}
				for sequence, frame := range lifecycleFrames(t, output.String()) {
					var gotSequence int
					if err := json.Unmarshal(frame["sequence_number"], &gotSequence); err != nil || gotSequence != sequence {
						t.Fatalf("sequence=%s expected=%d err=%v", frame["sequence_number"], sequence, err)
					}
					kind := frameString(t, frame, "type")
					if kind == "response.output_item.added" || kind == "response.output_item.done" {
						var item responsesOutputItem
						if err := json.Unmarshal(frame["item"], &item); err != nil {
							t.Fatal(err)
						}
						if kind == "response.output_item.added" {
							starts[item.ID]++
						} else {
							stops[item.ID]++
							if item.Arguments != got[item.CallID] {
								t.Fatalf("completed arguments=%q deltas=%q", item.Arguments, got[item.CallID])
							}
						}
					}
					if kind == "response.completed" {
						var response responsesResponse
						if err := json.Unmarshal(frame["response"], &response); err != nil {
							t.Fatal(err)
						}
						if response.ID != "msg_local" || response.Model != "upstream" || len(response.Output) != 2 {
							t.Fatalf("terminal=%s", frame["response"])
						}
					}
				}
				if len(starts) != 2 || !reflect.DeepEqual(starts, stops) {
					t.Fatalf("starts=%v stops=%v", starts, stops)
				}
			}
		})
	}
}

func TestResponsesArgumentSnapshots(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		events []string
		want   string
	}{
		{"announcement is not a prefix", []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item_a","call_id":"a","name":"alpha","arguments":"{}"}}`,
			`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"item_a","delta":"{\"n\":"}`,
			`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"item_a","delta":"9007199254740993}"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"item_a","call_id":"a","name":"alpha","arguments":"{\"n\":9007199254740993}"}}`,
		}, `{"n":9007199254740993}`},
		{"done only", []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item_a","call_id":"a","name":"alpha","arguments":"{}"}}`,
			`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"item_a","arguments":"{ \"n\": 2 }"}`,
			`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"item_a","arguments":"{ \"n\": 2 }"}`,
		}, `{ "n": 2 }`},
		{"terminal only", []string{
			`{"type":"response.completed","response":{"id":"resp_local","status":"completed","output":[{"type":"function_call","id":"item_a","call_id":"a","name":"alpha","arguments":"{ \"n\": 2 }"}]}}`,
		}, `{ "n": 2 }`},
	} {
		for _, destination := range []string{"chat", "messages"} {
			t.Run(fixture.name+"/"+destination, func(t *testing.T) {
				events := append([]string(nil), fixture.events...)
				if fixture.name != "terminal only" {
					events = append(events, `{"type":"response.completed","response":{"status":"completed"}}`)
				}
				var output bytes.Buffer
				var err error
				if destination == "chat" {
					err = ChatStreamFromResponses(&output, nil, "m").Consume(strings.NewReader(streamFixture(events...)))
				} else {
					err = NewResponsesStreamWriter(&output, nil, "m", false).Consume(strings.NewReader(streamFixture(events...)))
				}
				if err != nil {
					t.Fatal(err)
				}
				got := streamedToolArguments(t, destination, output.String())
				if !reflect.DeepEqual(got, map[string]string{"a": fixture.want}) {
					t.Fatalf("arguments=%v expected=%q", got, fixture.want)
				}
			})
		}
	}
}

func TestAnthropicStreamFailuresAndWrites(t *testing.T) {
	for _, destination := range []string{"chat", "responses"} {
		t.Run(destination, func(t *testing.T) {
			create := func(writer io.Writer, flush func()) (func(io.Reader) error, func(string), func()) {
				if destination == "chat" {
					s := NewChatStreamFromAnthropic(writer, flush, "m")
					return s.Consume, s.Fail, s.Finish
				}
				s := NewResponsesStreamFromAnthropic(writer, flush, "m")
				return s.Consume, s.Fail, s.Finish
			}
			for _, prefix := range []string{"", streamFixture(`{"type":"message_start","message":{"id":"msg_local"}}`)} {
				var output bytes.Buffer
				consume, fail, finish := create(&output, nil)
				err := consume(strings.NewReader(prefix + streamFixture(`{"type":"error","error":{"type":"overloaded_error","message":"local error"}}`)))
				var upstream *UpstreamError
				if !errors.As(err, &upstream) || upstream.Type != "overloaded_error" {
					t.Fatalf("err=%v", err)
				}
				if prefix == "" {
					if output.Len() != 0 {
						t.Fatalf("first error committed: %s", output.String())
					}
					continue
				}
				fail(err.Error())
				fail(err.Error())
				finish()
				failures := 0
				for _, frame := range lifecycleFrames(t, output.String()) {
					if len(frame["error"]) > 0 || string(frame["type"]) == `"response.failed"` {
						failures++
					}
					if string(frame["type"]) == `"response.completed"` {
						t.Fatal("failed stream completed")
					}
				}
				if failures != 1 || strings.Contains(output.String(), "[DONE]") {
					t.Fatalf("failures=%d output=%s", failures, output.String())
				}
			}
			flushes := 0
			consume, _, _ := create(lifecycleShortWriter{}, func() { flushes++ })
			err := consume(strings.NewReader(streamFixture(`{"type":"message_start","message":{}}`)))
			if !errors.Is(err, io.ErrShortWrite) || flushes != 0 {
				t.Fatalf("err=%v flushes=%d", err, flushes)
			}
		})
	}
}
