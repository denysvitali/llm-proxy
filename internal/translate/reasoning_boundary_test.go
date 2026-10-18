package translate

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestUnsignedThinkingStreamBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		newWriter func(io.Writer) interface {
			Consume(io.Reader) error
			Fail(string)
		}
		text, reasoning string
	}{
		{
			name: "chat",
			newWriter: func(w io.Writer) interface {
				Consume(io.Reader) error
				Fail(string)
			} {
				return NewStreamWriter(w, nil, "m", true)
			},
			text:      `{"id":"reply","choices":[{"delta":{"content":"answer"}}]}`,
			reasoning: `{"id":"reply","choices":[{"delta":{"reasoning_content":"private"}}]}`,
		},
		{
			name: "responses",
			newWriter: func(w io.Writer) interface {
				Consume(io.Reader) error
				Fail(string)
			} {
				return NewResponsesStreamWriter(w, nil, "m", true)
			},
			text:      `{"type":"response.output_text.delta","output_index":0,"delta":"answer"}`,
			reasoning: `{"type":"response.reasoning_text.delta","output_index":1,"delta":"private"}`,
		},
	} {
		for _, started := range []bool{false, true} {
			name := "before output"
			if started {
				name = "after output"
			}
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				var out bytes.Buffer
				writer := tc.newWriter(&out)
				stream := "data: " + tc.reasoning + "\n\n"
				if started {
					stream = "data: " + tc.text + "\n\n" + stream
				}
				err := writer.Consume(strings.NewReader(stream))
				if !errors.Is(err, errUnsignedThinking) {
					t.Fatalf("Consume = %v, want unsigned-thinking boundary", err)
				}
				writer.Fail(err.Error())
				writer.Fail(err.Error())
				events := parseSSE(t, out.String())
				failures := 0
				for _, event := range events {
					if event.Name == "error" {
						failures++
					}
					if event.Name == "message_stop" || event.Name == "message_delta" {
						t.Fatalf("failure became completion: %s", out.String())
					}
				}
				if started && failures != 1 || !started && len(events) != 0 {
					t.Fatalf("failure events = %d, started = %t: %s", failures, started, out.String())
				}
				for _, forbidden := range []string{"private", "thinking_delta", "signature_delta", `"type":"thinking"`} {
					if strings.Contains(out.String(), forbidden) {
						t.Fatalf("unsigned state leaked %q: %s", forbidden, out.String())
					}
				}
			})
		}
	}
}
