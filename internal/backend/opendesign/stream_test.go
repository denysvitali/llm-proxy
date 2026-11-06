package opendesign

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const (
	streamStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\"}}\n\n"
	blockStart  = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n"
	blockDelta  = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hm\"}}\n\n"
	blockStop   = "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	normalEnd   = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
)

func TestMaxTokensCompleter(t *testing.T) {
	limitEnd := "\nevent: message_delta\ndata: " +
		`{"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":16}}` +
		"\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, test := range []struct {
		name   string
		stream string
		want   string
	}{
		{"limit ending repaired", streamStart + blockStart + blockDelta + blockStop, streamStart + blockStart + blockDelta + blockStop + limitEnd},
		{"normal ending untouched", streamStart + blockStart + blockDelta + blockStop + normalEnd, streamStart + blockStart + blockDelta + blockStop + normalEnd},
		{"cut inside a block stays broken", streamStart + blockStart + blockDelta, streamStart + blockStart + blockDelta},
		{"no content stays broken", streamStart, streamStart},
		{"error event untouched", streamStart + blockStart + blockStop + "event: error\ndata: {\"type\":\"error\"}\n\n", streamStart + blockStart + blockStop + "event: error\ndata: {\"type\":\"error\"}\n\n"},
		{"empty body", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// One byte per read exercises frames split across reads.
			body := io.NopCloser(iotest.OneByteReader(strings.NewReader(test.stream)))
			got, err := io.ReadAll(newMaxTokensCompleter(body, []byte(`{"max_tokens":16,"stream":true}`)))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, test.want)
			}
		})
	}
}

func TestMaxTokensCompleterKeepsTransportErrors(t *testing.T) {
	broken := errors.New("connection reset")
	body := io.NopCloser(io.MultiReader(strings.NewReader(streamStart+blockStart+blockStop), iotest.ErrReader(broken)))
	got, err := io.ReadAll(newMaxTokensCompleter(body, nil))
	if !errors.Is(err, broken) {
		t.Fatalf("err = %v, want %v", err, broken)
	}
	if bytes.Contains(got, []byte("message_stop")) {
		t.Fatalf("synthesized an ending after a transport error: %s", got)
	}
}

func TestSendCompletesMaxTokensStreams(t *testing.T) {
	truncated := streamStart + blockStart + blockDelta + blockStop
	contentType := "text/event-stream"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, truncated)
	}))
	defer upstream.Close()
	c := New(upstream.URL, "k")
	defer c.HTTP.CloseIdleConnections()
	send := func(kind backend.Kind, streaming bool) string {
		t.Helper()
		resp, err := c.Send(context.Background(), &backend.Request{Kind: kind, Streaming: streaming, RawBody: []byte(`{"max_tokens":7}`)})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(got)
	}
	if got := send(backend.KindAnthropic, true); !strings.HasSuffix(got, "data: {\"type\":\"message_stop\"}\n\n") ||
		!strings.Contains(got, `"stop_reason":"max_tokens"`) || !strings.Contains(got, `"output_tokens":7`) {
		t.Errorf("anthropic stream not completed: %s", got)
	}
	if got := send(backend.KindOpenAIChat, true); got != truncated {
		t.Errorf("chat stream changed: %s", got)
	}
	contentType = "application/json"
	if got := send(backend.KindAnthropic, true); got != truncated {
		t.Errorf("non-SSE body changed: %s", got)
	}
}
