package apodex

import (
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
var _ backend.ModelWireOverrider = (*Client)(nil)

func TestSupports(t *testing.T) {
	c := New("", "tok")
	for _, tc := range []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindAnthropic, true},
		{backend.KindOpenAIChat, true},
		{backend.KindOpenAIResponses, true},
		{"", false},
	} {
		if got := c.Supports(tc.kind); got != tc.want {
			t.Errorf("Supports(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestSupportsModelForcesResponsesThroughChatTranslation(t *testing.T) {
	c := New("", "tok")
	for _, test := range []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindAnthropic, true},
		{backend.KindOpenAIChat, true},
		{backend.KindOpenAIResponses, false},
	} {
		if got := c.SupportsModel(test.kind, "apodex-1.1"); got != test.want {
			t.Errorf("SupportsModel(%q) = %v, want %v", test.kind, got, test.want)
		}
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if defaultBaseURL != "https://api.apodex.ai/v1" {
		t.Errorf("defaultBaseURL = %q, want %q", defaultBaseURL, "https://api.apodex.ai/v1")
	}
	if got := New("", "tok").BaseURL; got != defaultBaseURL {
		t.Errorf("New(\"\") BaseURL = %q, want %q", got, defaultBaseURL)
	}
	if got := New("https://example.test/v1/", "tok").BaseURL; got != "https://example.test/v1" {
		t.Errorf("New trailing slash BaseURL = %q, want %q", got, "https://example.test/v1")
	}
}

func TestSendRoutingHeadersAndBody(t *testing.T) {
	tests := []struct {
		name          string
		kind          backend.Kind
		wantPath      string
		streaming     bool
		wantAccept    string
		wantAnthropic bool
	}{
		{
			name:          "anthropic non-streaming",
			kind:          backend.KindAnthropic,
			wantPath:      "/messages",
			wantAccept:    "application/json",
			wantAnthropic: true,
		},
		{
			name:          "anthropic streaming",
			kind:          backend.KindAnthropic,
			wantPath:      "/messages",
			streaming:     true,
			wantAccept:    "text/event-stream",
			wantAnthropic: true,
		},
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
			const rawBody = `{"model":"apodex-1.1","messages":[]}`
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(b, &fields); err != nil {
					t.Fatalf("request body %q is not a JSON object: %v", b, err)
				}
				if string(fields["model"]) != `"apodex-1.1"` {
					t.Errorf("model field = %s, want %q", fields["model"], "apodex-1.1")
				}
				if string(fields["messages"]) != `[]` {
					t.Errorf("messages field = %s, want []", fields["messages"])
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
				gotVersion := h.Get("Anthropic-Version")
				if tt.wantAnthropic && gotVersion != anthropicVersion {
					t.Errorf("Anthropic-Version = %q, want %q", gotVersion, anthropicVersion)
				}
				if !tt.wantAnthropic && gotVersion != "" {
					t.Errorf("Anthropic-Version = %q on a non-Anthropic request, want it absent", gotVersion)
				}

				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, `{"ok":true}`)
			}))
			defer srv.Close()

			c := New(srv.URL, "test-token")
			resp, err := c.Send(context.Background(), &backend.Request{
				Kind:      tt.kind,
				Model:     "apodex-1.1",
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
			if string(body) != `{"ok":true}` {
				t.Errorf("response body = %q, want %q", body, `{"ok":true}`)
			}
		})
	}
}

// Apodex defaults stream to true for its deep-research models, so an omitted
// or stale stream field must be pinned to what the client actually asked for
// before the request leaves the proxy.
func TestSendPinsStreamOnOpenAIShapes(t *testing.T) {
	tests := []struct {
		name      string
		kind      backend.Kind
		body      string
		streaming bool
		want      string
	}{
		{
			name: "omitted stream becomes false",
			kind: backend.KindOpenAIChat,
			body: `{"model":"apodex-1-1-deep-research","messages":[]}`,
			want: `false`,
		},
		{
			name:      "omitted stream becomes true when streaming",
			kind:      backend.KindOpenAIChat,
			body:      `{"model":"apodex-1-1-deep-research","messages":[]}`,
			streaming: true,
			want:      `true`,
		},
		{
			name: "explicit false is preserved",
			kind: backend.KindOpenAIResponses,
			body: `{"model":"apodex-1-1-deep-research","input":"hi","stream":false}`,
			want: `false`,
		},
		{
			name:      "explicit true is preserved",
			kind:      backend.KindOpenAIResponses,
			body:      `{"model":"apodex-1-1-deep-research","input":"hi","stream":true}`,
			streaming: true,
			want:      `true`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(b, &fields); err != nil {
					t.Fatalf("request body %q is not a JSON object: %v", b, err)
				}
				got = string(fields["stream"])
				_, _ = fmt.Fprint(w, `{}`)
			}))
			defer srv.Close()

			c := New(srv.URL, "tok")
			resp, err := c.Send(context.Background(), &backend.Request{
				Kind:      tt.kind,
				Model:     "apodex-1-1-deep-research",
				RawBody:   []byte(tt.body),
				Streaming: tt.streaming,
			})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			_ = resp.Body.Close()
			if got != tt.want {
				t.Errorf("upstream stream field = %s, want %s", got, tt.want)
			}
		})
	}
}

// The Anthropic Messages API already defaults stream to false, so /messages
// bodies stay byte-for-byte identical.
func TestSendLeavesAnthropicBodyUntouched(t *testing.T) {
	const rawBody = `{"model":"apodex-1.1","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	resp, err := c.Send(context.Background(), &backend.Request{
		Kind:    backend.KindAnthropic,
		Model:   "apodex-1.1",
		RawBody: []byte(rawBody),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = resp.Body.Close()
	if string(got) != rawBody {
		t.Errorf("upstream body = %q, want byte-for-byte %q", got, rawBody)
	}
}

func TestSendNormalizesResponsesPromptRoles(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	resp, err := c.Send(context.Background(), &backend.Request{
		Kind:  backend.KindOpenAIResponses,
		Model: "apodex-1.1",
		RawBody: []byte(`{"model":"apodex-1.1","instructions":"base","input":[` +
			`{"type":"message","role":"developer","content":"skills"},` +
			`{"type":"message","role":"user","content":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = resp.Body.Close()

	var request struct {
		Instructions string `json:"instructions"`
		Input        []struct {
			Role string `json:"role"`
		} `json:"input"`
	}
	if err := json.Unmarshal(got, &request); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if request.Instructions != "base\n\nskills" {
		t.Errorf("instructions = %q", request.Instructions)
	}
	if len(request.Input) != 1 || request.Input[0].Role != "user" {
		t.Errorf("upstream input = %+v, want only the user item", request.Input)
	}
}

func TestSendPreservesResponsesClientTools(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	resp, err := c.Send(context.Background(), &backend.Request{
		Kind:    backend.KindOpenAIResponses,
		Model:   "apodex-1.1",
		RawBody: []byte(`{"model":"apodex-1.1","input":"hi","tools":[{"type":"function","name":"shell","parameters":{}}]}`),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(got), `"name":"shell"`) {
		t.Errorf("client tool was removed from request: %s", got)
	}
}

func TestWithExplicitStreamLeavesNonObjectsAlone(t *testing.T) {
	for _, body := range []string{`[1,2]`, `not json`, ``} {
		if got := withExplicitStream([]byte(body), true); string(got) != body {
			t.Errorf("withExplicitStream(%q) = %q, want it unchanged", body, got)
		}
	}
}

func TestSendUnsupportedKind(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		t.Errorf("HTTP handler reached for unsupported kind at %q; Apodex must reject it without an upstream call", r.URL.Path)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	resp, err := c.Send(context.Background(), &backend.Request{Kind: "", RawBody: []byte(`{}`)})
	if err == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("Send with empty kind returned nil error, want unsupported-kind error")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Errorf("error = %q, want message containing %q", err, "does not support")
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
	const payload = `{"data":[
		{"id":"apodex-1.1","context_length":262144},
		{"id":""},
		{"id":"apodex-1.1-mini","context_length":262144}
	]}`
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
			want := []string{"apodex-1.1", "apodex-1.1-mini"}
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
		StatusCode: http.StatusPaymentRequired,
		Body: &readCloserRecorder{
			Reader:  strings.NewReader("insufficient credits"),
			onClose: func() { closed = true },
		},
	}
	he := ReadError(httpResp)

	if he.Status != http.StatusPaymentRequired {
		t.Errorf("HTTPError.Status = %d, want %d", he.Status, http.StatusPaymentRequired)
	}
	if !closed {
		t.Error("ReadError did not close the response body")
	}
	if got := string(he.Body); got != "insufficient credits" {
		t.Errorf("HTTPError.Body = %q, want fully drained %q", got, "insufficient credits")
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
