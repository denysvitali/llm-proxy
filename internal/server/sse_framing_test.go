package server

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestNativeCompletionUsesCompleteFrames(t *testing.T) {
	for _, tt := range []struct {
		name     string
		kind     backend.Kind
		body     string
		complete bool
	}{
		{"chat", backend.KindOpenAIChat, "data:[DONE]\n\n", true},
		{"chat text", backend.KindOpenAIChat, "data: {\"choices\":[{\"delta\":{\"content\":\"data: [DONE]\"}}]}\n\n", false},
		// Newline-terminated [DONE] without the blank line still completes:
		// the data line is whole, only the frame separator is missing.
		{"chat done without blank line", backend.KindOpenAIChat, "data: [DONE]\n", true},
		{"chat truncated data line", backend.KindOpenAIChat, "data: [DON", false},
		{"chat comment", backend.KindOpenAIChat, ": data: [DONE]\n\n", false},
		{"messages", backend.KindAnthropic, "event: message_stop\r\ndata: {\"type\" : \"message_stop\"}\r\n\r\n", true},
		{"messages text", backend.KindAnthropic, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"event: message_stop\"}}\n\n", false},
		{"messages mismatched header", backend.KindAnthropic, "event: message_stop\ndata: {\"type\":\"content_block_delta\"}\n\n", false},
		{"messages error", backend.KindAnthropic, "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n", true},
		{"responses multiline", backend.KindOpenAIResponses, "event: response.completed\ndata: {\ndata: \"type\": \"response.completed\",\ndata: \"response\":{\"status\":\"completed\"}}\n\n", true},
		{"responses nested marker", backend.KindOpenAIResponses, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"response.completed\"}}\n\n", false},
		{"responses malformed", backend.KindOpenAIResponses, "event: response.completed\ndata: {\"type\":\"response.completed\"\n\n", false},
		{"responses failed", backend.KindOpenAIResponses, "data: {\"type\":\"response.failed\"}\n\n", true},
		{"responses incomplete", backend.KindOpenAIResponses, "data: {\"type\":\"response.incomplete\"}\n\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, size := range []int{1, 3, 32 * 1024} {
				var output bytes.Buffer
				flushes := 0
				reader := &fragmentReader{data: []byte(tt.body), size: size}
				n, complete, err := pipeSSE(&output, func() { flushes++ }, reader, streamDoneChecker(tt.kind))
				if !errors.Is(err, io.EOF) || complete != tt.complete || n != int64(len(tt.body)) || output.String() != tt.body || flushes == 0 {
					t.Fatalf("fragment %d: bytes=%d complete=%v err=%v output=%q flushes=%d", size, n, complete, err, output.String(), flushes)
				}
			}
		})
	}
}

func TestNativeStreamWriteFailureStopsReading(t *testing.T) {
	failure := errors.New("client gone")
	for _, writeErr := range []error{nil, failure} {
		reader := &fragmentReader{data: []byte("data: partial\n\ndata: [DONE]\n\n"), size: 4}
		n, complete, err := pipeSSE(shortStreamWriter{err: writeErr}, nil, reader, streamDoneChecker(backend.KindOpenAIChat))
		want := writeErr
		if want == nil {
			want = io.ErrShortWrite
		}
		if !errors.Is(err, want) || n != 1 || complete || reader.reads != 1 {
			t.Fatalf("n=%d complete=%v err=%v reads=%d", n, complete, err, reader.reads)
		}
	}
}

func TestNativeStreamOversizeFrame(t *testing.T) {
	body := "data: " + strings.Repeat("x", 8<<20)
	_, complete, err := pipeSSE(io.Discard, nil, strings.NewReader(body), streamDoneChecker(backend.KindOpenAIChat))
	if err == nil || complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}

type fragmentReader struct {
	data  []byte
	size  int
	reads int
}

func (r *fragmentReader) Read(p []byte) (int, error) {
	r.reads++
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data[:min(r.size, len(r.data))])
	r.data = r.data[n:]
	return n, nil
}

type shortStreamWriter struct{ err error }

func (w shortStreamWriter) Write([]byte) (int, error) { return 1, w.err }
