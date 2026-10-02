package translate

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestScanSSEFramingAndTerminalEvent(t *testing.T) {
	// Providers use CRLF or LF, may emit keepalives, and sometimes omit the
	// final newline. Decoding and terminal markers belong to the consumer.
	body := ": heartbeat\r\nevent: delta\r\ndata:  {\"text\":\"hi\"} \r\n\r\ndata:\n" +
		"data:[DONE]\ndata: must not be decoded"
	var payloads []string
	err := scanSSE(strings.NewReader(body), func(payload []byte) (bool, error) {
		payloads = append(payloads, string(payload))
		return string(payload) == "[DONE]", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`{"text":"hi"}`, "[DONE]"}; !reflect.DeepEqual(payloads, want) {
		t.Fatalf("payloads = %q, want %q", payloads, want)
	}
	var last string
	if err := scanSSE(strings.NewReader("data: last"), func(payload []byte) (bool, error) {
		last = string(payload)
		return false, nil
	}); err != nil || last != "last" {
		t.Fatalf("unterminated line = %q, %v", last, err)
	}
}

func TestScanSSEPropagatesFailures(t *testing.T) {
	failure := errors.New("broken stream")
	t.Run("reader", func(t *testing.T) {
		body := io.MultiReader(strings.NewReader("data: first\n\n"), sseErrorReader{failure})
		if err := scanSSE(body, func([]byte) (bool, error) { return false, nil }); !errors.Is(err, failure) {
			t.Fatalf("reader error = %v, want %v", err, failure)
		}
	})
	t.Run("decoder", func(t *testing.T) {
		if err := scanSSE(strings.NewReader("data: failed\n"), func([]byte) (bool, error) {
			return false, failure
		}); !errors.Is(err, failure) {
			t.Fatalf("decoder error = %v, want %v", err, failure)
		}
	})
	t.Run("line limit", func(t *testing.T) {
		called := false
		err := scanSSE(strings.NewReader("data: "+strings.Repeat("x", maxStreamLine)), func([]byte) (bool, error) {
			called = true
			return false, nil
		})
		if err == nil || called {
			t.Fatalf("oversized line: err=%v, decoded=%v", err, called)
		}
	})
}

type sseErrorReader struct{ err error }

func (r sseErrorReader) Read([]byte) (int, error) { return 0, r.err }
