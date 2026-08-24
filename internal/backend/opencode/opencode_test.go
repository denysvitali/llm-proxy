package opencode

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// Compile-time interface conformance.
var _ backend.Backend = (*Client)(nil)

func TestDefaultBaseURL(t *testing.T) {
	const want = "https://opencode.ai/zen/v1"
	if defaultBaseURL != want {
		t.Fatalf("defaultBaseURL = %q, want %q", defaultBaseURL, want)
	}
	if got := New("", "key").BaseURL; got != want {
		t.Fatalf(`New("").BaseURL = %q, want %q`, got, want)
	}
}

func TestHasAPIKey(t *testing.T) {
	if New("", "").HasAPIKey() {
		t.Error(`New("", "").HasAPIKey() = true, want false`)
	}
	if !New("", "sk-test").HasAPIKey() {
		t.Error(`New("", "sk-test").HasAPIKey() = false, want true`)
	}
}

func TestSupports(t *testing.T) {
	tests := []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindAnthropic, true},
		{backend.KindOpenAIChat, true},
		{backend.KindOpenAIResponses, false},
		{backend.Kind("carrier-pigeon"), false},
	}
	for _, tt := range tests {
		if got := (&Client{}).Supports(tt.kind); got != tt.want {
			t.Errorf("Supports(%q) = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestSupportsModel(t *testing.T) {
	tests := []struct {
		kind  backend.Kind
		model string
		want  bool
	}{
		// Zen's Anthropic /messages endpoint only serves Claude models;
		// everything else 500s there and must be translated to Chat.
		{backend.KindAnthropic, "claude-sonnet-5", true},
		{backend.KindAnthropic, "claude-opus-4-8", true},
		{backend.KindAnthropic, "opencode/claude-haiku-4-5", true},
		{backend.KindAnthropic, "x-preview-f-free", false},
		{backend.KindAnthropic, "opencode/x-preview-f-free", false},
		{backend.KindAnthropic, "gpt-5.4-nano", false},
		{backend.KindAnthropic, "deepseek-v4-flash-free", false},
		{backend.KindAnthropic, "big-pickle", false},
		// Chat Completions is served for every model.
		{backend.KindOpenAIChat, "claude-sonnet-5", true},
		{backend.KindOpenAIChat, "x-preview-f-free", true},
		{backend.KindOpenAIChat, "gpt-5.4-nano", true},
		// Responses is never native.
		{backend.KindOpenAIResponses, "gpt-5.4-nano", false},
	}
	for _, tt := range tests {
		if got := (&Client{}).SupportsModel(tt.kind, tt.model); got != tt.want {
			t.Errorf("SupportsModel(%q, %q) = %v, want %v", tt.kind, tt.model, got, tt.want)
		}
	}
}

// Client must satisfy the optional per-model wire override.
var _ backend.ModelWireOverrider = (*Client)(nil)

// recordedRequest captures what the test server received.
type recordedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// newRecordingClient returns a Client pointed at a test server that records
// each request and replies with the given status/content-type/body.
func newRecordingClient(t *testing.T, status int, contentType, replyBody string) (*Client, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		rec.Method = r.Method
		rec.Path = r.URL.Path
		rec.Header = r.Header.Clone()
		rec.Body = body
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		if _, err := io.WriteString(w, replyBody); err != nil {
			t.Errorf("write response body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "test-key")
	c.HTTP = srv.Client()
	return c, rec
}

func TestSendRouting(t *testing.T) {
	tests := []struct {
		kind     backend.Kind
		wantPath string
	}{
		{backend.KindAnthropic, "/messages"},
		{backend.KindOpenAIChat, "/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			c, rec := newRecordingClient(t, http.StatusOK, "application/json", `{}`)
			resp, err := c.Send(t.Context(), &backend.Request{
				Kind:    tt.kind,
				Model:   "some-model",
				RawBody: []byte(`{"x":1}`),
				Header:  http.Header{},
			})
			if err != nil {
				t.Fatalf("Send(%q) returned error: %v", tt.kind, err)
			}
			defer func() { _ = resp.Body.Close() }()

			if rec.Method != http.MethodPost {
				t.Errorf("method = %q, want POST", rec.Method)
			}
			if rec.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", rec.Path, tt.wantPath)
			}
			if resp.Status != http.StatusOK {
				t.Errorf("resp.Status = %d, want %d", resp.Status, http.StatusOK)
			}
		})
	}
}

func TestSendUnsupportedKind(t *testing.T) {
	c, _ := newRecordingClient(t, http.StatusOK, "application/json", `{}`)
	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:    backend.KindOpenAIResponses,
		RawBody: []byte(`{}`),
	})
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		t.Fatalf("Send(KindOpenAIResponses) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Errorf("error %q does not mention \"does not support\"", err)
	}
}

func TestSendAnthropicHeaders(t *testing.T) {
	c, rec := newRecordingClient(t, http.StatusOK, "application/json", `{"ok":true}`)
	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:    backend.KindAnthropic,
		Model:   "claude-opus-5",
		RawBody: []byte(`{"model":"claude-opus-5"}`),
		Header:  http.Header{},
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got, want := rec.Header.Get("Anthropic-Version"), "2023-06-01"; got != want {
		t.Errorf("Anthropic-Version = %q, want %q", got, want)
	}
	if got, want := rec.Header.Get("Authorization"), "Bearer test-key"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	if got, want := rec.Header.Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := rec.Header.Get("Accept"), "application/json"; got != want {
		t.Errorf("Accept = %q, want %q", got, want)
	}
}

func TestSendAuthorization(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		wantAuth string // empty means the header must be absent
	}{
		{"with key", "test-key", "Bearer test-key"},
		{"without key (free models)", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newRecordingClient(t, http.StatusOK, "application/json", `{}`)
			c.Key = tt.key
			resp, err := c.Send(t.Context(), &backend.Request{
				Kind:    backend.KindOpenAIChat,
				RawBody: []byte(`{}`),
			})
			if err != nil {
				t.Fatalf("Send returned error: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if got := rec.Header.Get("Authorization"); got != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, tt.wantAuth)
			}
		})
	}
}

func TestSendAcceptSwitchesOnStreaming(t *testing.T) {
	tests := []struct {
		name      string
		streaming bool
		want      string
	}{
		{"non-streaming", false, "application/json"},
		{"streaming", true, "text/event-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newRecordingClient(t, http.StatusOK, "text/plain", "")
			resp, err := c.Send(t.Context(), &backend.Request{
				Kind:      backend.KindAnthropic,
				RawBody:   []byte(`{}`),
				Streaming: tt.streaming,
			})
			if err != nil {
				t.Fatalf("Send returned error: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if got := rec.Header.Get("Accept"); got != tt.want {
				t.Errorf("Accept = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendRawBodyForwardedByteForByte(t *testing.T) {
	c, rec := newRecordingClient(t, http.StatusOK, "application/json", `{}`)
	// Trailing newline and multi-byte runes must survive untouched.
	raw := []byte("{\"model\":\"m\",\"input\":\"line1\\nline2 — ünïcode\"}\n")
	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:    backend.KindOpenAIChat,
		RawBody: raw,
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if len(rec.Body) != len(raw) {
		t.Fatalf("forwarded body length = %d, want %d (%q)", len(rec.Body), len(raw), rec.Body)
	}
	if !bytes.Equal(rec.Body, raw) {
		t.Errorf("forwarded body = %q, want %q", rec.Body, raw)
	}
}

func TestModelsFiltersEmptyIDsAndWorksWithoutKey(t *testing.T) {
	c, rec := newRecordingClient(t, http.StatusOK, "application/json",
		`{"data":[{"id":"gemini-3-flash"},{"id":""},{"id":"claude-opus-5"}]}`)
	c.Key = ""

	models, err := c.Models(t.Context())
	if err != nil {
		t.Fatalf("Models returned error: %v", err)
	}

	want := []string{"gemini-3-flash", "claude-opus-5"}
	if !slices.Equal(models, want) {
		t.Errorf("Models = %v, want %v", models, want)
	}
	if rec.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", rec.Method)
	}
	if rec.Path != "/models" {
		t.Errorf("path = %q, want /models", rec.Path)
	}
	if got := rec.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want absent", got)
	}
}

func TestModelsHTTPError(t *testing.T) {
	c, _ := newRecordingClient(t, http.StatusBadGateway, "text/plain", "upstream exploded")

	_, err := c.Models(t.Context())
	if err == nil {
		t.Fatal("Models succeeded against a 502, want error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error type = %T, want *HTTPError (err: %v)", err, err)
	}
	if httpErr.Status != http.StatusBadGateway {
		t.Errorf("HTTPError.Status = %d, want %d", httpErr.Status, http.StatusBadGateway)
	}
	if string(httpErr.Body) != "upstream exploded" {
		t.Errorf("HTTPError.Body = %q, want %q", httpErr.Body, "upstream exploded")
	}
}
