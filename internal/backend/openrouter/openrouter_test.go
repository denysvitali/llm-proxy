package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
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
	tests := []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindOpenAIChat, true},
		{backend.KindAnthropic, false},
		{backend.KindOpenAIResponses, false},
		{"", false},
	}
	for _, test := range tests {
		if got := client.Supports(test.kind); got != test.want {
			t.Errorf("Supports(%q) = %v, want %v", test.kind, got, test.want)
		}
	}
}

func TestSend(t *testing.T) {
	const requestBody = `{"model":"vendor/model","messages":[]}`
	const responseBody = `{"id":"response-id"}`
	tests := []struct {
		name       string
		streaming  bool
		wantAccept string
	}{
		{name: "non-streaming", wantAccept: "application/json"},
		{name: "streaming", streaming: true, wantAccept: "text/event-stream"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotMethod, gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				received, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if !bytes.Equal(received, []byte(requestBody)) {
					t.Errorf("request body = %q, want byte-for-byte %q", received, requestBody)
				}
				checks := []struct {
					header string
					want   string
				}{
					{"Authorization", "Bearer secret"},
					{"Content-Type", "application/json"},
					{"Accept", test.wantAccept},
				}
				for _, check := range checks {
					if got := r.Header.Get(check.header); got != check.want {
						t.Errorf("%s = %q, want %q", check.header, got, check.want)
					}
				}
				_, _ = fmt.Fprint(w, responseBody)
			}))
			defer server.Close()

			response, err := New(server.URL, "secret").Send(context.Background(), &backend.Request{
				Kind:      backend.KindOpenAIChat,
				Model:     "vendor/model",
				RawBody:   []byte(requestBody),
				Streaming: test.streaming,
			})
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if gotMethod != http.MethodPost || gotPath != "/chat/completions" {
				t.Errorf("request = %s %s, want POST /chat/completions", gotMethod, gotPath)
			}
			if response.Status != http.StatusOK {
				t.Errorf("status = %d, want %d", response.Status, http.StatusOK)
			}
			received, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if !bytes.Equal(received, []byte(responseBody)) {
				t.Errorf("response body = %q, want passthrough body %q", received, responseBody)
			}
		})
	}
}

func TestSendRejectsUnsupportedKindAndMissingKey(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	client := New(server.URL, "")
	request := &backend.Request{Kind: backend.KindOpenAIChat, RawBody: []byte(`{}`)}
	if _, err := client.Send(context.Background(), request); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("Send without key error = %v, want missing-key error", err)
	}
	client.Key = "secret"
	request.Kind = backend.KindAnthropic
	if _, err := client.Send(context.Background(), request); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("Send Anthropic error = %v, want unsupported-kind error", err)
	}
	if called {
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
		_, _ = fmt.Fprint(w, `{"data":[{"id":"vendor/model"},{"id":""},{"id":"other/model"}]}`)
	}))
	defer server.Close()

	models, err := New(server.URL, "secret").Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	want := []string{"vendor/model", "other/model"}
	if !reflect.DeepEqual(models, want) {
		t.Errorf("Models() = %#v, want %#v", models, want)
	}
}

func TestModelsHTTPErrors(t *testing.T) {
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
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("Models() malformed JSON error = %v, want decode error", err)
	}
}
