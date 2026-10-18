package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestUsageBufferedStreamMatrix(t *testing.T) {
	usage := map[string]string{
		"messages":  `{"input_tokens":11,"output_tokens":13,"cache_read_input_tokens":7,"cache_creation_input_tokens":5,"output_tokens_details":{"reasoning_tokens":3}}`,
		"chat":      `{"prompt_tokens":23,"completion_tokens":13,"total_tokens":36,"prompt_tokens_details":{"cached_tokens":7,"cache_creation_tokens":5},"completion_tokens_details":{"reasoning_tokens":3}}`,
		"responses": `{"input_tokens":23,"output_tokens":13,"total_tokens":36,"input_tokens_details":{"cached_tokens":7,"cache_creation_tokens":5},"output_tokens_details":{"reasoning_tokens":3}}`,
	}
	bodies := map[string]string{
		"messages":  `{"id":"msg_test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":%s}`,
		"chat":      `{"id":"chat_test","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":%s}`,
		"responses": `{"id":"resp_test","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":%s}`,
	}
	streams := map[string]string{
		"messages":  "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":%s}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":13}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
		"chat":      "data: {\"id\":\"chat_test\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":%s}\n\ndata: [DONE]\n\n",
		"responses": "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"usage\":%s}}\n\n",
	}
	type consumer interface{ Consume(io.Reader) error }
	pairs := []struct {
		from, to string
		buffered func([]byte, string) ([]byte, error)
		stream   func(io.Writer) consumer
	}{
		{"messages", "chat", ChatFromAnthropic, func(w io.Writer) consumer { return NewChatStreamFromAnthropic(w, nil, "m") }},
		{"messages", "responses", ResponsesFromAnthropic, func(w io.Writer) consumer { return NewResponsesStreamFromAnthropic(w, nil, "m") }},
		{"chat", "messages", func(b []byte, m string) ([]byte, error) { return FromOpenAI(b, m, false) }, func(w io.Writer) consumer { return NewStreamWriter(w, nil, "m", false) }},
		{"chat", "responses", ResponsesFromChat, func(w io.Writer) consumer { return NewResponsesStreamFromChat(w, nil, "m") }},
		{"responses", "messages", func(b []byte, m string) ([]byte, error) { return FromResponses(b, m, false) }, func(w io.Writer) consumer { return NewResponsesStreamWriter(w, nil, "m", false) }},
		{"responses", "chat", ChatFromResponses, func(w io.Writer) consumer { return ChatStreamFromResponses(w, nil, "m") }},
	}
	for _, pair := range pairs {
		t.Run(pair.from+"_to_"+pair.to, func(t *testing.T) {
			body, err := pair.buffered([]byte(fmt.Sprintf(bodies[pair.from], usage[pair.from])), "m")
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Usage map[string]any `json:"usage"`
			}
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(usage[pair.to]), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Usage, want) {
				t.Fatalf("buffered usage = %#v; want %#v", decoded.Usage, want)
			}
			var output bytes.Buffer
			if err := pair.stream(&output).Consume(strings.NewReader(fmt.Sprintf(streams[pair.from], usage[pair.from]))); err != nil {
				t.Fatal(err)
			}
			got := terminalUsage(t, output.String())
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("stream usage = %#v; want %#v", got, want)
			}
		})
	}
}

func terminalUsage(t *testing.T, stream string) map[string]any {
	t.Helper()
	var usage map[string]any
	for _, line := range strings.Split(stream, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if response, ok := event["response"].(map[string]any); ok {
			event = response
		}
		if current, ok := event["usage"].(map[string]any); ok {
			usage = current
		}
	}
	if usage == nil {
		t.Fatal("missing terminal usage")
	}
	return usage
}

func TestAnthropicUsageCumulativeDeltas(t *testing.T) {
	for _, destination := range []string{"chat", "responses"} {
		t.Run(destination, func(t *testing.T) {
			input := `data: {"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":1,"cache_read_input_tokens":7,"cache_creation_input_tokens":5}}}

data: {"type":"message_delta","usage":{"input_tokens":0,"output_tokens":9,"cache_read_input_tokens":0,"cache_creation_input_tokens":2,"output_tokens_details":{"reasoning_tokens":3}}}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}

data: {"type":"message_stop"}

`
			var output bytes.Buffer
			var err error
			if destination == "chat" {
				err = NewChatStreamFromAnthropic(&output, nil, "m").Consume(strings.NewReader(input))
			} else {
				err = NewResponsesStreamFromAnthropic(&output, nil, "m").Consume(strings.NewReader(input))
			}
			if err != nil {
				t.Fatal(err)
			}
			got := terminalUsage(t, output.String())
			in, out, detail := "input_tokens", "output_tokens", "input_tokens_details"
			if destination == "chat" {
				in, out, detail = "prompt_tokens", "completion_tokens", "prompt_tokens_details"
			}
			if got[in] != float64(2) || got[out] != float64(9) || got["total_tokens"] != float64(11) {
				t.Fatalf("usage = %v", got)
			}
			details := got[detail].(map[string]any)
			if details["cached_tokens"] != float64(0) || details["cache_creation_tokens"] != float64(2) {
				t.Fatalf("details = %v", details)
			}
		})
	}
}

func TestUsageRejectsInconsistentCounters(t *testing.T) {
	for _, usage := range []string{
		`{"input_tokens":2,"input_tokens_details":{"cached_tokens":3}}`,
		`{"input_tokens":2,"input_tokens_details":{"cached_tokens":1,"cache_creation_tokens":2}}`,
		`{"input_tokens":-1}`,
		`{"output_tokens":2,"output_tokens_details":{"reasoning_tokens":3}}`,
		`{"input_tokens":9223372036854775807,"output_tokens":1}`,
	} {
		body := []byte(`{"status":"completed","output":[],"usage":` + usage + `}`)
		if _, err := FromResponses(body, "m", false); err == nil {
			t.Errorf("Messages accepted %s", usage)
		}
		if _, err := ChatFromResponses(body, "m"); err == nil {
			t.Errorf("Chat accepted %s", usage)
		}
		stream := `data: {"type":"response.completed","response":` + string(body) + "}\n\n"
		var output bytes.Buffer
		if err := NewResponsesStreamWriter(&output, nil, "m", false).Consume(strings.NewReader(stream)); err == nil {
			t.Errorf("Messages stream accepted %s", usage)
		}
		if err := ChatStreamFromResponses(&output, nil, "m").Consume(strings.NewReader(stream)); err == nil {
			t.Errorf("Chat stream accepted %s", usage)
		}
		if output.Len() != 0 {
			t.Errorf("invalid first usage committed output: %s", output.String())
		}
	}
	for _, usage := range []string{`{"input_tokens":-1}`, `{"input_tokens":9223372036854775807,"cache_read_input_tokens":1}`} {
		body := []byte(`{"content":[],"usage":` + usage + `}`)
		if _, err := ChatFromAnthropic(body, "m"); err == nil {
			t.Errorf("Chat accepted %s", usage)
		}
		if _, err := ResponsesFromAnthropic(body, "m"); err == nil {
			t.Errorf("Responses accepted %s", usage)
		}
	}
	body := []byte(`{"choices":[],"usage":{"prompt_tokens":2,"prompt_tokens_details":{"cached_tokens":3}}}`)
	if _, err := FromOpenAI(body, "m", false); err == nil {
		t.Fatal("Messages accepted invalid Chat usage")
	}
	if _, err := ResponsesFromChat(body, "m"); err == nil {
		t.Fatal("Responses accepted invalid Chat usage")
	}
	stream := "data: " + string(body) + "\n\ndata: [DONE]\n\n"
	var output bytes.Buffer
	if err := NewStreamWriter(&output, nil, "m", false).Consume(strings.NewReader(stream)); err == nil {
		t.Fatal("Messages stream accepted invalid Chat usage")
	}
	if err := NewResponsesStreamFromChat(&output, nil, "m").Consume(strings.NewReader(stream)); err == nil {
		t.Fatal("Responses stream accepted invalid Chat usage")
	}
	if output.Len() != 0 {
		t.Fatal("invalid first Chat usage committed output")
	}
	stream = "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":-1}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	if err := NewChatStreamFromAnthropic(&output, nil, "m").Consume(strings.NewReader(stream)); err == nil {
		t.Fatal("Chat stream accepted invalid Messages usage delta")
	}
	output.Reset()
	if err := NewResponsesStreamFromAnthropic(&output, nil, "m").Consume(strings.NewReader(stream)); err == nil {
		t.Fatal("Responses stream accepted invalid Messages usage delta")
	}
	if strings.Contains(output.String(), "response.completed") {
		t.Fatal("invalid usage completed stream")
	}
}

func TestUsageZeroAndRoundTrip(t *testing.T) {
	for _, original := range []AnthropicUsage{
		{},
		{CacheReadInputTokens: 7, CacheCreationInputTokens: 5},
		{InputTokens: 11, OutputTokens: 13, CacheReadInputTokens: 7, CacheCreationInputTokens: 5, OutputTokensDetails: &reasoningTokensDetail{ReasoningTokens: 3}},
	} {
		canonical := anthropicUsageToResponses(original)
		got := responsesUsageToAnthropic(chatUsageToResponses(responsesUsageToChat(canonical)))
		if !reflect.DeepEqual(got, original) {
			t.Fatalf("round trip = %+v; want %+v", got, original)
		}
		encoded, err := json.Marshal(canonical)
		if err != nil {
			t.Fatal(err)
		}
		var shape map[string]any
		if err := json.Unmarshal(encoded, &shape); err != nil {
			t.Fatal(err)
		}
		if len(shape) != 5 || len(shape["output_tokens_details"].(map[string]any)) != 1 {
			t.Fatalf("usage shape = %s", encoded)
		}
	}
}
