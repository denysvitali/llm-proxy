package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// Native requests must retain fields and formatting the proxy does not
// understand; sharing request preparation must not turn passthrough into
// decoding and re-encoding a subset of a provider's API.
func TestRequestPipelineNativePreservesBody(t *testing.T) {
	for _, tc := range []struct {
		path string
		kind backend.Kind
	}{
		{"/v1/messages", backend.KindAnthropic},
		{"/v1/chat/completions", backend.KindOpenAIChat},
		{"/v1/responses", backend.KindOpenAIResponses},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			fb := &fakeOABackend{
				name:  "native",
				body:  `{"ok":true}`,
				kinds: map[backend.Kind]bool{tc.kind: true},
			}
			s := newOATestServer(t, fb, map[string]config.ModelRoute{
				"alias": {Backend: "native", Model: "upstream"},
			})
			body := "{\n  \"model\": \"alias\",\n  \"stream\": false,\n  \"provider_options\": {\"model\": \"alias\", \"verbatim\": \"\\u0061\"},\n  \"messages\": [], \"input\": []\n}"
			rec := postOpenAI(t, s, tc.path, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			got := fb.lastRequest()
			if got == nil {
				t.Fatal("no request reached backend")
			}
			wantBody := strings.Replace(body, `"model": "alias"`, `"model": "upstream"`, 1)
			if string(got.RawBody) != wantBody {
				t.Errorf("forwarded body = %q, want %q", got.RawBody, wantBody)
			}
			if got.Kind != tc.kind || got.Model != "upstream" || got.Streaming {
				t.Errorf("forwarded routing = %s/%s, streaming = %t", got.Kind, got.Model, got.Streaming)
			}
		})
	}
}

// The APIs intentionally differ in their validation and error envelopes.
// Keep those client contracts explicit while sharing routing orchestration.
func TestRequestPipelineValidationDialects(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, message, errorType string
		status                               int
	}{
		{"messages JSON", "/v1/messages", "{", "request body must be a JSON object", "invalid_request_error", 400},
		{"chat JSON", "/v1/chat/completions", "{", "request body is not valid JSON", "invalid_request_error", 400},
		{"responses JSON", "/v1/responses", "{", "request body is not valid JSON", "invalid_request_error", 400},
		{"messages missing model", "/v1/messages", `{}`, "model is required", "invalid_request_error", 400},
		{"chat missing model", "/v1/chat/completions", `{}`, `model "" has no available backend`, "invalid_request_error", 404},
		{"responses missing model", "/v1/responses", `{}`, `model "" has no available backend`, "invalid_request_error", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := &fakeOABackend{name: "unused"}
			s := newOATestServer(t, fb, nil)
			rec := postOpenAI(t, s, tc.path, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var response struct {
				Type  string `json:"type"`
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Type != tc.errorType || response.Error.Message != tc.message {
				t.Errorf("error = %+v", response.Error)
			}
			if tc.path == "/v1/messages" && response.Type != "error" {
				t.Errorf("Anthropic error type = %q", response.Type)
			}
			if tc.status == http.StatusNotFound && response.Error.Code != "model_not_found" {
				t.Errorf("OpenAI error code = %q", response.Error.Code)
			}
			if fb.lastRequest() != nil {
				t.Error("invalid request reached backend")
			}
		})
	}
}

func TestRequestPipelineAnthropicThinking(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name, thinking := "disabled", ""
		if enabled {
			name, thinking = "enabled", `,"thinking":{"type":"enabled","budget_tokens":64}`
		}
		t.Run(name, func(t *testing.T) {
			fb := &fakeOABackend{
				name:  "chat",
				kinds: map[backend.Kind]bool{backend.KindOpenAIChat: true},
				body:  `{"id":"reply","choices":[{"message":{"role":"assistant","content":"answer","reasoning_content":"reasoning"},"finish_reason":"stop"}]}`,
			}
			s := newOATestServer(t, fb, map[string]config.ModelRoute{
				"alias": {Backend: "chat", Model: "upstream"},
			})
			rec := postOpenAI(t, s, "/v1/messages", `{"model":"alias","max_tokens":128,"messages":[{"role":"user","content":"hello"}]`+thinking+`}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var response struct {
				Content []struct {
					Type     string `json:"type"`
					Thinking string `json:"thinking"`
				} `json:"content"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, block := range response.Content {
				if block.Type == "thinking" && block.Thinking == "reasoning" {
					found = true
				}
			}
			if found != enabled {
				t.Errorf("thinking block present = %t, want %t; body = %s", found, enabled, rec.Body.String())
			}
		})
	}
}

func TestRequestPipelineAnthropicTranslationValidation(t *testing.T) {
	fb := &fakeOABackend{
		name:  "chat",
		kinds: map[backend.Kind]bool{backend.KindOpenAIChat: true},
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"alias": {Backend: "chat", Model: "upstream"},
	})
	rec := postOpenAI(t, s, "/v1/messages", `{"model":"alias","max_tokens":"invalid","messages":[]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid Anthropic Messages request:") {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if fb.lastRequest() != nil {
		t.Error("invalid translated request reached backend")
	}
}
