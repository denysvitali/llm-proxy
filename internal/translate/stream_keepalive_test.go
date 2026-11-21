package translate

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// Reasoning deltas produce no Messages event, so a model thinking for minutes
// would leave the client's byte watchdog nothing to see; the writer pings
// instead, at most once per interval, and never outside the open message.
func TestStreamWriterPingsThroughSilentReasoning(t *testing.T) {
	var reasoning strings.Builder
	for range 6 {
		reasoning.WriteString(`data: {"id":"c","choices":[{"index":0,"delta":{"reasoning_content":"hmm"}}]}` + "\n\n")
	}
	stream := `data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
		reasoning.String() +
		`data: {"id":"c","choices":[{"index":0,"delta":{"content":"42"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	var out bytes.Buffer
	writer := NewStreamWriter(&out, func() {}, "m", false)
	clock := time.Unix(0, 0)
	writer.now = func() time.Time { return clock }
	// Each upstream chunk arrives 4s after the previous one: 24s of silent
	// reasoning, so two pings (at >=10s gaps), not six.
	body := &steppedReader{data: []byte(stream), step: func() { clock = clock.Add(4 * time.Second) }}
	if err := writer.Consume(body); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if pings := strings.Count(got, "event: ping"); pings != 2 {
		t.Fatalf("pings = %d, want 2:\n%s", pings, got)
	}
	if strings.Index(got, "event: ping") < strings.Index(got, "event: message_start") ||
		strings.LastIndex(got, "event: ping") > strings.Index(got, "event: message_stop") {
		t.Fatalf("ping outside the open message:\n%s", got)
	}
	if strings.Contains(got, "hmm") || !strings.Contains(got, `"text":"42"`) {
		t.Fatalf("stream content wrong:\n%s", got)
	}
}

// steppedReader hands out one SSE frame per Read, advancing the test clock
// before each, as a slow upstream would.
type steppedReader struct {
	data []byte
	step func()
}

func (r *steppedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	r.step()
	end := bytes.Index(r.data, []byte("\n\n"))
	if end < 0 {
		end = len(r.data)
	} else {
		end += 2
	}
	n := copy(p, r.data[:end])
	r.data = r.data[n:]
	return n, nil
}
