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

func lifecycleFrames(t *testing.T, output string) []map[string]json.RawMessage {
	t.Helper()
	var frames []map[string]json.RawMessage
	if err := scanSSE(strings.NewReader(output), func(data []byte) (bool, error) {
		if string(data) == "[DONE]" {
			return false, nil
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal(data, &event); err != nil {
			return false, err
		}
		frames = append(frames, event)
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	return frames
}

func frameString(t *testing.T, frame map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(frame[key], &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestChatMessagesInterleavedLifecycle(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":7,"id":"a","function":{"name":"alpha","arguments":"{\"a\":"}},{"index":3,"id":"b","function":{"name":"beta","arguments":"{\"b\":"}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":3,"id":"b","function":{"name":"beta","arguments":"2}"}},{"index":7,"id":"a","function":{"name":"alpha","arguments":"1}"}}]}}]}`,
		`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`, "",
	}, "\n\n")
	var output bytes.Buffer
	stream := NewStreamWriter(&output, nil, "m", false)
	if err := stream.Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	stream.Finish()
	stream.Fail("must not emit")
	starts := map[int]string{}
	arguments := map[int]string{}
	stops := map[int]int{}
	terminals := 0
	for _, frame := range lifecycleFrames(t, output.String()) {
		var index int
		if raw := frame["index"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &index); err != nil {
				t.Fatal(err)
			}
		}
		switch frameString(t, frame, "type") {
		case "content_block_start":
			var block struct{ ID string }
			if err := json.Unmarshal(frame["content_block"], &block); err != nil {
				t.Fatal(err)
			}
			if starts[index] != "" {
				t.Fatalf("duplicate start: %s", output.String())
			}
			starts[index] = block.ID
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
		case "message_stop":
			terminals++
		case "error":
			t.Fatalf("unexpected error: %s", output.String())
		}
	}
	if !reflect.DeepEqual(starts, map[int]string{0: "toolu_a", 1: "toolu_b"}) ||
		!reflect.DeepEqual(arguments, map[int]string{0: `{"a":1}`, 1: `{"b":2}`}) ||
		!reflect.DeepEqual(stops, map[int]int{0: 1, 1: 1}) || terminals != 1 {
		t.Fatalf("starts=%v args=%v stops=%v terminals=%d", starts, arguments, stops, terminals)
	}
}

type lifecycleShortWriter struct{}

func (lifecycleShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestChatMessagesErrorsAndWriteFailure(t *testing.T) {
	t.Run("typed first event", func(t *testing.T) {
		var output bytes.Buffer
		stream := NewStreamWriter(&output, nil, "m", false)
		err := stream.Consume(strings.NewReader("data: {\"error\":{\"type\":\"invalid_request_error\",\"code\":\"context_length_exceeded\",\"message\":\"local error\"}}\n\n"))
		var upstream *UpstreamError
		if !errors.As(err, &upstream) || !upstream.InvalidRequest() || output.Len() != 0 {
			t.Fatalf("err=%v output=%s", err, output.String())
		}
	})
	t.Run("post output one failure", func(t *testing.T) {
		var output bytes.Buffer
		stream := NewStreamWriter(&output, nil, "m", false)
		input := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"error\":{\"type\":\"overloaded_error\",\"message\":\"local error\"}}\n\n"
		err := stream.Consume(strings.NewReader(input))
		if err == nil {
			t.Fatal("missing failure")
		}
		stream.Fail(err.Error())
		stream.Fail(err.Error())
		stream.Finish()
		failures := 0
		for _, frame := range lifecycleFrames(t, output.String()) {
			switch frameString(t, frame, "type") {
			case "error":
				failures++
			case "message_stop":
				t.Fatal("failed stream completed")
			}
		}
		if failures != 1 {
			t.Fatalf("failures=%d", failures)
		}
	})
	t.Run("short write", func(t *testing.T) {
		flushes := 0
		stream := NewStreamWriter(lifecycleShortWriter{}, func() { flushes++ }, "m", false)
		err := stream.Consume(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		if !errors.Is(err, io.ErrShortWrite) || flushes != 0 {
			t.Fatalf("err=%v flushes=%d", err, flushes)
		}
	})
}
