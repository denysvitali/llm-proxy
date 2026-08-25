package grok

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

var _ backend.Backend = (*Client)(nil)

type staticToken string

func (t staticToken) AccessToken(context.Context) (string, error) { return string(t), nil }

func TestSupports(t *testing.T) {
	c := New("", staticToken("tok"))
	for _, kind := range []backend.Kind{
		backend.KindOpenAIResponses,
		backend.KindOpenAIChat,
		backend.KindAnthropic,
		"",
	} {
		got := c.Supports(kind)
		want := kind == backend.KindOpenAIResponses
		if got != want {
			t.Errorf("Supports(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if defaultBaseURL != "https://cli-chat-proxy.grok.com/v1" {
		t.Errorf("defaultBaseURL = %q, want %q", defaultBaseURL, "https://cli-chat-proxy.grok.com/v1")
	}
	if got := New("", staticToken("tok")).BaseURL; got != defaultBaseURL {
		t.Errorf("New(\"\") BaseURL = %q, want %q", got, defaultBaseURL)
	}
}

func TestSendHeadersAndBody(t *testing.T) {
	const rawBody = `{"model":"grok-4.5","input":"hi"}`
	tests := []struct {
		name       string
		streaming  bool
		wantAccept string
	}{
		{name: "non-streaming", streaming: false, wantAccept: "application/json"},
		{name: "streaming", streaming: true, wantAccept: "text/event-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if !bytes.Equal(b, []byte(rawBody)) {
					t.Errorf("request body = %q, want byte-for-byte %q", b, rawBody)
				}

				h := r.Header
				checks := []struct {
					header string
					want   string
				}{
					{"Authorization", "Bearer test-token"},
					{"X-XAI-Token-Auth", "xai-grok-cli"},
					{"x-grok-client-version", ClientVersion},
					{"x-grok-client-mode", "cli"},
					{"Content-Type", "application/json"},
					{"Accept", tt.wantAccept},
				}
				for _, chk := range checks {
					if got := h.Get(chk.header); got != chk.want {
						t.Errorf("header %s = %q, want %q", chk.header, got, chk.want)
					}
				}
				if ua := h.Get("User-Agent"); !strings.HasPrefix(ua, "llm-proxy/") {
					t.Errorf("User-Agent = %q, want prefix %q", ua, "llm-proxy/")
				}

				w.Header().Set("Content-Type", tt.wantAccept)
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, `{"ok":true}`)
			}))
			defer srv.Close()

			c := New(srv.URL, staticToken("test-token"))
			resp, err := c.Send(context.Background(), &backend.Request{
				Kind:      backend.KindOpenAIResponses,
				Model:     "grok-4.5",
				RawBody:   []byte(rawBody),
				Streaming: tt.streaming,
			})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if gotPath != "/responses" {
				t.Errorf("request path = %q, want %q", gotPath, "/responses")
			}
			if resp.Status != http.StatusOK {
				t.Errorf("status = %d, want %d", resp.Status, http.StatusOK)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if !bytes.Equal(body, []byte(`{"ok":true}`)) {
				t.Errorf("response body = %q, want %q", body, `{"ok":true}`)
			}
		})
	}
}

func TestSendEmptyToken(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		t.Error("HTTP handler reached despite empty token")
	}))
	defer srv.Close()

	c := New(srv.URL, staticToken(""))
	resp, err := c.Send(context.Background(), &backend.Request{
		Kind:    backend.KindOpenAIResponses,
		RawBody: []byte(`{}`),
	})
	if err == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("Send with empty token returned nil error, want error before any HTTP call")
	}
	if reached {
		t.Error("handler was invoked; Send must fail before any HTTP call")
	}
}

func TestModelsIncludesGrok46(t *testing.T) {
	c := New("", staticToken("tok"))
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	want := map[string]bool{
		"grok-4.5":               false,
		"grok-4.6":               false,
		"grok-composer-2.5-fast": false,
	}
	for _, model := range models {
		if _, known := want[model]; !known {
			t.Errorf("Models() returned unexpected model %q", model)
			continue
		}
		want[model] = true
	}
	for model, found := range want {
		if !found {
			t.Errorf("Models() missing %q", model)
		}
	}
}

func TestSendNon2xxReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := New(srv.URL, staticToken("test-token"))
	resp, err := c.Send(context.Background(), &backend.Request{
		Kind:    backend.KindOpenAIResponses,
		RawBody: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.Status, http.StatusBadGateway)
	}

	// Wrap the passthrough Response back into an *http.Response so ReadError
	// can drain it, mirroring what the server layer does on non-2xx.
	body, ok := resp.Body.(io.ReadCloser)
	if !ok {
		t.Fatalf("response body type %T does not implement io.ReadCloser", resp.Body)
	}
	httpResp := &http.Response{StatusCode: resp.Status, Header: resp.Header, Body: body}
	httpErr := ReadError(httpResp)
	wrapped := fmt.Errorf("sending to grok: %w", httpErr)
	var he *HTTPError
	if !errors.As(wrapped, &he) {
		t.Fatalf("errors.As(*HTTPError) failed for %v (%T)", wrapped, wrapped)
	}
	if he.Status != http.StatusBadGateway {
		t.Errorf("HTTPError.Status = %d, want %d", he.Status, http.StatusBadGateway)
	}
	if got := strings.TrimSpace(string(he.Body)); got != "upstream exploded" {
		t.Errorf("HTTPError.Body = %q, want %q", got, "upstream exploded")
	}
}
