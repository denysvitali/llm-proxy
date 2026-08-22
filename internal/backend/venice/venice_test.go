package venice

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

func TestSupports(t *testing.T) {
	c := New("", "tok")
	for _, tc := range []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindOpenAIChat, true},
		{backend.KindOpenAIResponses, false},
		{backend.KindAnthropic, false},
		{"", false},
	} {
		if got := c.Supports(tc.kind); got != tc.want {
			t.Errorf("Supports(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if defaultBaseURL != "https://api.venice.ai/api/v1" {
		t.Errorf("defaultBaseURL = %q, want %q", defaultBaseURL, "https://api.venice.ai/api/v1")
	}
	if got := New("", "tok").BaseURL; got != defaultBaseURL {
		t.Errorf("New(\"\") BaseURL = %q, want %q", got, defaultBaseURL)
	}
}

func TestSendRoutingHeadersAndBody(t *testing.T) {
	const rawBody = `{"model":"venice-uncensored","messages":[]}`
	tests := []struct {
		name       string
		kind       backend.Kind
		wantPath   string
		streaming  bool
		wantAccept string
	}{
		{
			name:       "openai chat non-streaming",
			kind:       backend.KindOpenAIChat,
			wantPath:   "/chat/completions",
			wantAccept: "application/json",
		},
		{
			name:       "openai chat streaming",
			kind:       backend.KindOpenAIChat,
			wantPath:   "/chat/completions",
			streaming:  true,
			wantAccept: "text/event-stream",
		},
		{
			name:       "openai responses non-streaming",
			kind:       backend.KindOpenAIResponses,
			wantPath:   "/responses",
			wantAccept: "application/json",
		},
		{
			name:       "openai responses streaming",
			kind:       backend.KindOpenAIResponses,
			wantPath:   "/responses",
			streaming:  true,
			wantAccept: "text/event-stream",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
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
					{"Content-Type", "application/json"},
					{"Accept", tt.wantAccept},
				}
				for _, chk := range checks {
					if got := h.Get(chk.header); got != chk.want {
						t.Errorf("header %s = %q, want %q", chk.header, got, chk.want)
					}
				}

				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, `{"ok":true}`)
			}))
			defer srv.Close()

			c := New(srv.URL, "test-token")
			resp, err := c.Send(context.Background(), &backend.Request{
				Kind:      tt.kind,
				Model:     "venice-uncensored",
				RawBody:   []byte(rawBody),
				Streaming: tt.streaming,
			})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if gotMethod != http.MethodPost {
				t.Errorf("request method = %q, want POST", gotMethod)
			}
			if gotPath != tt.wantPath {
				t.Errorf("request path = %q, want %q", gotPath, tt.wantPath)
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

func TestSendUnsupportedKind(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		t.Errorf("HTTP handler reached for kind %q; Venice must reject it without an upstream call", r.URL.Path)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	for _, kind := range []backend.Kind{backend.KindAnthropic, ""} {
		resp, err := c.Send(context.Background(), &backend.Request{
			Kind:    kind,
			RawBody: []byte(`{}`),
		})
		if err == nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			t.Errorf("Send(%q) returned nil error, want unsupported-kind error", kind)
			continue
		}
		if !strings.Contains(err.Error(), "does not support") {
			t.Errorf("Send(%q) error = %q, want message containing %q", kind, err, "does not support")
		}
	}
	if reached {
		t.Error("handler was invoked for unsupported kind")
	}
}

func TestSendEmptyKey(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		t.Error("HTTP handler reached despite empty key")
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	resp, err := c.Send(context.Background(), &backend.Request{
		Kind:    backend.KindOpenAIChat,
		RawBody: []byte(`{}`),
	})
	if err == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("Send with empty key returned nil error, want error before any HTTP call")
	}
	if reached {
		t.Error("handler was invoked; Send must fail before any HTTP call")
	}
}

func TestModelsFiltersEmptyIDsAndAuth(t *testing.T) {
	const payload = `{"data":[{"id":"a"},{"id":""},{"id":"b"}]}`
	tests := []struct {
		name       string
		key        string
		wantHeader bool
	}{
		{name: "with key sends authorization", key: "test-token", wantHeader: true},
		{name: "without key omits authorization", key: "", wantHeader: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/models" {
					t.Errorf("request path = %q, want %q", r.URL.Path, "/models")
				}
				if _, ok := r.Header["Authorization"]; ok != tt.wantHeader {
					t.Errorf("Authorization header present = %v, want %v", ok, tt.wantHeader)
				}
				if tt.wantHeader && r.Header.Get("Authorization") != "Bearer "+tt.key {
					t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer "+tt.key)
				}
				_, _ = fmt.Fprint(w, payload)
			}))
			defer srv.Close()

			c := New(srv.URL, tt.key)
			models, err := c.Models(context.Background())
			if err != nil {
				t.Fatalf("Models: %v", err)
			}
			want := []string{"a", "b"}
			if !reflect.DeepEqual(models, want) {
				t.Errorf("Models() = %#v, want %#v", models, want)
			}
		})
	}
}

func TestModelsNon2xxHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid api key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "bad-token")
	models, err := c.Models(context.Background())
	if err == nil {
		t.Fatal("Models returned nil error for non-2xx response")
	}
	if models != nil {
		t.Errorf("Models() = %#v alongside error, want nil", models)
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("errors.As(*HTTPError) failed for %v (%T)", err, err)
	}
	if he.Status != http.StatusUnauthorized {
		t.Errorf("HTTPError.Status = %d, want %d", he.Status, http.StatusUnauthorized)
	}
	if got := strings.TrimSpace(string(he.Body)); got != "invalid api key" {
		t.Errorf("HTTPError.Body = %q, want %q", got, "invalid api key")
	}
}

func TestReadErrorDrainsAndPreservesStatus(t *testing.T) {
	closed := false
	httpResp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body: &readCloserRecorder{
			Reader:  strings.NewReader("upstream exploded\nwith details"),
			onClose: func() { closed = true },
		},
	}
	he := ReadError(httpResp)

	if he.Status != http.StatusBadGateway {
		t.Errorf("HTTPError.Status = %d, want %d", he.Status, http.StatusBadGateway)
	}
	if !closed {
		t.Error("ReadError did not close the response body")
	}
	if got := string(he.Body); got != "upstream exploded\nwith details" {
		t.Errorf("HTTPError.Body = %q, want fully drained %q", got, "upstream exploded\nwith details")
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
