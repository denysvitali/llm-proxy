package mimotokenplan

import (
	"bytes"
	"context"
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

func TestDefaultsAndSupports(t *testing.T) {
	client := New("", "tp-test")
	if client.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", client.BaseURL, defaultBaseURL)
	}

	tests := []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindOpenAIChat, true},
		{backend.KindOpenAIResponses, true},
		{backend.KindAnthropic, false},
		{backend.Kind("unknown"), false},
	}
	for _, test := range tests {
		if got := client.Supports(test.kind); got != test.want {
			t.Errorf("Supports(%q) = %v, want %v", test.kind, got, test.want)
		}
	}
}

func TestSendUsesOnlyOpenAIEndpoints(t *testing.T) {
	const requestBody = `{"model":"mimo-v2.5-pro","input":"hello"}`
	tests := []struct {
		name       string
		kind       backend.Kind
		streaming  bool
		wantPath   string
		wantAccept string
	}{
		{name: "chat", kind: backend.KindOpenAIChat, wantPath: "/chat/completions", wantAccept: "application/json"},
		{name: "responses stream", kind: backend.KindOpenAIResponses, streaming: true, wantPath: "/responses", wantAccept: "text/event-stream"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.wantPath {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, test.wantPath)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				if !bytes.Equal(body, []byte(requestBody)) {
					t.Errorf("body = %q, want byte-for-byte %q", body, requestBody)
				}
				if got := r.Header.Get("api-key"); got != "tp-secret" {
					t.Errorf("api-key = %q, want tp-secret", got)
				}
				if got := r.Header.Get("Authorization"); got != "" {
					t.Errorf("Authorization = %q, want empty", got)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
				if got := r.Header.Get("Accept"); got != test.wantAccept {
					t.Errorf("Accept = %q, want %q", got, test.wantAccept)
				}
				_, _ = fmt.Fprint(w, `{"ok":true}`)
			}))
			defer server.Close()

			client := New(server.URL+"/", "tp-secret")
			response, err := client.Send(context.Background(), &backend.Request{
				Kind:      test.kind,
				RawBody:   []byte(requestBody),
				Streaming: test.streaming,
			})
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.Status != http.StatusOK {
				t.Errorf("status = %d, want 200", response.Status)
			}
		})
	}
}

func TestSendRejectsAnthropicAndMissingKey(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	client := New(server.URL, "")
	request := &backend.Request{Kind: backend.KindOpenAIResponses, RawBody: []byte(`{}`)}
	if _, err := client.Send(context.Background(), request); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("missing-key error = %v, want actionable error", err)
	}
	client.Key = "tp-secret"
	request.Kind = backend.KindAnthropic
	if _, err := client.Send(context.Background(), request); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("Anthropic error = %v, want unsupported-kind error", err)
	}
	if called {
		t.Error("upstream was called for a rejected request")
	}
}

func TestModels(t *testing.T) {
	client := New("", "tp-test")
	got, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	want := []string{"mimo-v2.5-pro", "mimo-v2.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Models() = %#v, want %#v", got, want)
	}
	got[0] = "mutated"
	again, _ := client.Models(context.Background())
	if !reflect.DeepEqual(again, want) {
		t.Errorf("Models() exposed mutable package state: %#v", again)
	}
}
