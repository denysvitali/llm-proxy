package opencodego

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

var _ backend.Backend = (*Client)(nil)
var _ backend.ModelWireOverrider = (*Client)(nil)

func TestDefaultBaseURL(t *testing.T) {
	const want = "https://opencode.ai/zen/go/v1"
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
	if !New("", "test-key").HasAPIKey() {
		t.Error(`New("", "test-key").HasAPIKey() = false, want true`)
	}
}

func TestSupportsModel(t *testing.T) {
	c := &Client{}
	tests := []struct {
		model string
		kind  backend.Kind
		want  bool
	}{
		{"grok-4.6", backend.KindOpenAIResponses, true},
		{"gpt-5.6-luna", backend.KindOpenAIResponses, true},
		{"muse-spark-1.2-contributor", backend.KindOpenAIResponses, true},
		{"opencode-go/grok-4.6", backend.KindOpenAIResponses, true},
		{"minimax-m3", backend.KindAnthropic, true},
		{"qwen3.7-plus", backend.KindAnthropic, true},
		{"kimi-k3", backend.KindOpenAIChat, true},
		{"new-chat-model", backend.KindOpenAIChat, true},
		{"grok-4.6", backend.KindOpenAIChat, false},
		{"minimax-m3", backend.KindOpenAIChat, false},
	}
	for _, tt := range tests {
		if got := c.SupportsModel(tt.kind, tt.model); got != tt.want {
			t.Errorf("SupportsModel(%q, %q) = %v, want %v", tt.kind, tt.model, got, tt.want)
		}
	}
}

type recordedRequest struct {
	method string
	path   string
	header http.Header
	body   []byte
}

func newRecordingClient(t *testing.T, status int, reply string) (*Client, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.header = r.Header.Clone()
		rec.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "test-key")
	c.HTTP = srv.Client()
	return c, rec
}

func TestSendRoutesByModelProtocol(t *testing.T) {
	tests := []struct {
		model string
		kind  backend.Kind
		path  string
	}{
		{"grok-4.6", backend.KindOpenAIResponses, "/responses"},
		{"kimi-k3", backend.KindOpenAIChat, "/chat/completions"},
		{"minimax-m3", backend.KindAnthropic, "/messages"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			c, rec := newRecordingClient(t, http.StatusOK, `{}`)
			resp, err := c.Send(t.Context(), &backend.Request{
				Kind:      tt.kind,
				Model:     tt.model,
				RawBody:   []byte(`{"model":"` + tt.model + `"}`),
				Streaming: true,
			})
			if err != nil {
				t.Fatalf("Send returned error: %v", err)
			}
			_ = resp.Body.Close()
			if rec.method != http.MethodPost || rec.path != tt.path {
				t.Errorf("request = %s %s, want POST %s", rec.method, rec.path, tt.path)
			}
			if got := rec.header.Get("Authorization"); got != "Bearer test-key" {
				t.Errorf("Authorization = %q, want bearer key", got)
			}
			if got := rec.header.Get("Accept"); got != "text/event-stream" {
				t.Errorf("Accept = %q, want text/event-stream", got)
			}
			if tt.kind == backend.KindAnthropic {
				if got := rec.header.Get("Anthropic-Version"); got != anthropicVersion {
					t.Errorf("Anthropic-Version = %q, want %q", got, anthropicVersion)
				}
			} else if got := rec.header.Get("Anthropic-Version"); got != "" {
				t.Errorf("Anthropic-Version = %q, want absent", got)
			}
		})
	}
}

func TestSendRejectsWrongProtocol(t *testing.T) {
	c, _ := newRecordingClient(t, http.StatusOK, `{}`)
	_, err := c.Send(t.Context(), &backend.Request{Kind: backend.KindOpenAIChat, Model: "grok-4.6"})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("Send wrong protocol error = %v, want unsupported error", err)
	}
}

func TestModelsFiltersEmptyIDsAndSendsKey(t *testing.T) {
	c, rec := newRecordingClient(t, http.StatusOK, `{"data":[{"id":"grok-4.6"},{"id":""},{"id":"kimi-k3"}]}`)
	models, err := c.Models(t.Context())
	if err != nil {
		t.Fatalf("Models returned error: %v", err)
	}
	if want := []string{"grok-4.6", "kimi-k3"}; !slices.Equal(models, want) {
		t.Errorf("Models = %v, want %v", models, want)
	}
	if rec.method != http.MethodGet || rec.path != "/models" {
		t.Errorf("request = %s %s, want GET /models", rec.method, rec.path)
	}
	if got := rec.header.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q, want bearer key", got)
	}
}

func TestModelsHTTPError(t *testing.T) {
	c, _ := newRecordingClient(t, http.StatusBadGateway, "upstream exploded")
	_, err := c.Models(t.Context())
	if err == nil {
		t.Fatal("Models succeeded against a 502")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadGateway {
		t.Fatalf("Models error = %T %v, want *HTTPError 502", err, err)
	}
}

func TestReadError(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"nope"}`)),
	}
	err := ReadError(resp)
	if err.Status != http.StatusUnauthorized || string(err.Body) != `{"error":"nope"}` {
		t.Fatalf("ReadError = %+v, want status/body", err)
	}
}

func TestSendStripsAdditionalToolsOnResponses(t *testing.T) {
	c, rec := newRecordingClient(t, http.StatusOK, `{}`)
	body := []byte(`{
		"model":"grok-4.6",
		"input":[
			{"type":"additional_tools","tools":[{"type":"web_search"}]},
			{"type":"message","role":"user","content":"hello"}
		]
	}`)
	resp, err := c.Send(t.Context(), &backend.Request{
		Kind:    backend.KindOpenAIResponses,
		Model:   "grok-4.6",
		RawBody: body,
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	_ = resp.Body.Close()
	if strings.Contains(string(rec.body), `"additional_tools"`) {
		t.Fatalf("additional_tools was forwarded: %s", rec.body)
	}
	if !strings.Contains(string(rec.body), `"hello"`) {
		t.Fatalf("user message missing after normalize: %s", rec.body)
	}
}
