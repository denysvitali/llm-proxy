package abliteration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

var _ backend.Backend = (*Client)(nil)

func TestSupportsAndDefaults(t *testing.T) {
	client := New("", "token")
	if client.BaseURL != defaultBaseURL {
		t.Errorf(`New("") BaseURL = %q, want %q`, client.BaseURL, defaultBaseURL)
	}
	if got := New("https://example.test/v1/", "token").BaseURL; got != "https://example.test/v1" {
		t.Errorf("New trailing slash BaseURL = %q, want %q", got, "https://example.test/v1")
	}

	for _, test := range []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindAnthropic, true},
		{backend.KindOpenAIChat, true},
		{backend.KindOpenAIResponses, true},
		{"", false},
	} {
		if got := client.Supports(test.kind); got != test.want {
			t.Errorf("Supports(%q) = %v, want %v", test.kind, got, test.want)
		}
	}
}

func TestSendNativeEndpoints(t *testing.T) {
	const requestBody = `{"model":"abliterated-model","messages":[]}`
	const responseBody = `{"id":"response-id"}`
	tests := []struct {
		name             string
		kind             backend.Kind
		path             string
		streaming        bool
		wantAccept       string
		wantAnthropicVer string
	}{
		{
			name:             "anthropic non-streaming",
			kind:             backend.KindAnthropic,
			path:             "/messages",
			wantAccept:       "application/json",
			wantAnthropicVer: anthropicVersion,
		},
		{
			name:       "chat completions streaming",
			kind:       backend.KindOpenAIChat,
			path:       "/chat/completions",
			streaming:  true,
			wantAccept: "text/event-stream",
		},
		{
			name:       "responses non-streaming",
			kind:       backend.KindOpenAIResponses,
			path:       "/responses",
			wantAccept: "application/json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, test.path)
				}
				received, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if !bytes.Equal(received, []byte(requestBody)) {
					t.Errorf("request body = %q, want byte-for-byte %q", received, requestBody)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer secret" {
					t.Errorf("Authorization = %q, want %q", got, "Bearer secret")
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
				if got := r.Header.Get("Accept"); got != test.wantAccept {
					t.Errorf("Accept = %q, want %q", got, test.wantAccept)
				}
				if got := r.Header.Get("Anthropic-Version"); got != test.wantAnthropicVer {
					t.Errorf("Anthropic-Version = %q, want %q", got, test.wantAnthropicVer)
				}
				_, _ = fmt.Fprint(w, responseBody)
			}))
			defer server.Close()

			response, err := New(server.URL, "secret").Send(context.Background(), &backend.Request{
				Kind:      test.kind,
				Model:     "abliterated-model",
				RawBody:   []byte(requestBody),
				Streaming: test.streaming,
			})
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.Status != http.StatusOK {
				t.Errorf("status = %d, want %d", response.Status, http.StatusOK)
			}
			received, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if !bytes.Equal(received, []byte(responseBody)) {
				t.Errorf("response body = %q, want %q", received, responseBody)
			}
		})
	}
}

func TestSendRejectsMissingKeyAndUnsupportedKind(t *testing.T) {
	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	defer server.Close()

	client := New(server.URL, "")
	if _, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIChat}); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("Send without key error = %v, want missing-key error", err)
	}

	client.Key = "secret"
	if _, err := client.Send(context.Background(), &backend.Request{Kind: "unsupported"}); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("Send unsupported kind error = %v, want unsupported-kind error", err)
	}
	if reached {
		t.Error("upstream called for invalid credentials or unsupported kind")
	}
}

func TestModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("request = %s %s, want GET /models", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"abliterated-model"},{"id":""},{"id":"abliterated-model-large"}]}`)
	}))
	defer server.Close()

	models, err := New(server.URL, "secret").Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	want := []string{"abliterated-model", "abliterated-model-large"}
	if !reflect.DeepEqual(models, want) {
		t.Errorf("Models() = %#v, want %#v", models, want)
	}
}

func TestModelsErrors(t *testing.T) {
	const payload = `{"error":{"message":"invalid key"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, payload)
	}))
	defer server.Close()

	_, err := New(server.URL, "secret").Models(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Models() error = %v, want *HTTPError", err)
	}
	if httpErr.Status != http.StatusUnauthorized || !bytes.Equal(httpErr.Body, []byte(payload)) {
		t.Errorf("HTTPError = %+v, want status 401 and original body", httpErr)
	}

	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "{bad")
	}))
	defer badJSONServer.Close()
	_, err = New(badJSONServer.URL, "secret").Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode models") {
		t.Fatalf("Models() malformed JSON error = %v, want decode error", err)
	}
}

func TestReadErrorDrainsAndPreservesStatus(t *testing.T) {
	closed := false
	response := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body: &readCloserRecorder{
			Reader:  strings.NewReader("upstream exploded\nwith details"),
			onClose: func() { closed = true },
		},
	}
	httpErr := ReadError(response)

	if httpErr.Status != http.StatusBadGateway {
		t.Errorf("HTTPError.Status = %d, want %d", httpErr.Status, http.StatusBadGateway)
	}
	if !closed {
		t.Error("ReadError did not close the response body")
	}
	if got := string(httpErr.Body); got != "upstream exploded\nwith details" {
		t.Errorf("HTTPError.Body = %q, want fully drained body", got)
	}
}

type readCloserRecorder struct {
	io.Reader
	onClose func()
}

func (r *readCloserRecorder) Close() error {
	r.onClose()
	return nil
}
