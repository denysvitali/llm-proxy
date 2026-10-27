package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/codex"
	"github.com/denysvitali/llm-proxy/internal/config"
)

type codexErrorToken struct{}

func (codexErrorToken) AccessToken(context.Context) (string, error) { return "test-token", nil }

// Exercise the real Codex SSE aggregation and each client dialect. A large
// response prefix reproduces the loss of the useful error in stored summaries.
func TestCodexContextErrorClientAPIs(t *testing.T) {
	runCodexFailureClientAPIs(t, []codexFailureCase{{
		name: "context error", code: "context_length_exceeded", kind: "invalid_request_error",
		message: "Your input exceeds the context window of this model. Please adjust your input and try again.",
	}})
}

func TestCodexCyberPolicyClientAPIs(t *testing.T) {
	runCodexFailureClientAPIs(t, []codexFailureCase{
		{name: "cyber policy missing type", code: "cyber_policy", message: "Request blocked by cyber policy."},
		{name: "cyber policy invalid request type", code: "cyber_policy", kind: "invalid_request", message: "Request blocked by cyber policy."},
		{name: "cyber policy server error type", code: "cyber_policy", kind: "server_error", message: "Request blocked by cyber policy."},
	})
}

type codexFailureCase struct {
	name, code, kind, message string
}

func runCodexFailureClientAPIs(t *testing.T, cases []codexFailureCase) {
	t.Helper()
	clients := []struct{ path, fields string }{
		{"/v1/messages", `"max_tokens":128,"messages":[{"role":"user","content":"Hello"}]`},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"Hello"}]`},
		{"/v1/responses", `"input":"Hello"`},
	}
	for _, failure := range cases {
		for _, client := range clients {
			for _, streaming := range []bool{false, true} {
				for _, started := range []bool{false, true} {
					for _, eventType := range []string{"response.failed", "error"} {
						t.Run(fmt.Sprintf("%s/%s/stream=%t/started=%t/%s", failure.name, client.path, streaming, started, eventType), func(t *testing.T) {
							var calls atomic.Int32
							upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								calls.Add(1)
								w.Header().Set("Content-Type", "text/event-stream")
								if started {
									_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-6-luna\"}}\n\n")
								}
								errObj := map[string]any{"code": failure.code, "message": failure.message}
								if failure.kind != "" {
									errObj["type"] = failure.kind
								}
								event := map[string]any{"type": eventType, "error": errObj}
								if eventType == "response.failed" {
									delete(event, "error")
									event["response"] = map[string]any{"id": "resp_1", "status": "failed", "instructions": strings.Repeat("private-prompt", 100), "error": errObj}
								}
								payload, _ := json.Marshal(event)
								_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
							}))
							defer upstream.Close()
							b := codex.New(upstream.URL, codexErrorToken{})
							b.HTTP = upstream.Client()
							fallback := newNamedScripted("second", backend.KindOpenAIResponses, step{resp: sseResponse("text/event-stream", "data: [DONE]\n\n")})
							s := newFallbackServer(t, b, fallback, config.BackendConfig{Type: "codex"}, map[string]config.ModelRoute{
								"m1": {Backend: "codex", Model: "gpt-6-luna", Fallbacks: []config.FallbackRoute{{Backend: "second", Model: "fallback-model"}}},
							})
							body := fmt.Sprintf(`{"model":"m1","stream":%t,%s}`, streaming, client.fields)
							rec := postOpenAI(t, s, client.path, body)
							wantStatus := http.StatusBadRequest
							if streaming && (started || client.path == "/v1/responses") {
								wantStatus = http.StatusOK // Headers are already committed.
							}
							if rec.Code != wantStatus || calls.Load() != 1 || fallback.callCount() != 0 {
								t.Fatalf("status=%d want=%d upstream calls=%d fallback calls=%d body=%s", rec.Code, wantStatus, calls.Load(), fallback.callCount(), rec.Body)
							}
							if !strings.Contains(rec.Body.String(), failure.message) {
								t.Fatalf("missing provider message: %s", rec.Body)
							}
							if streaming && client.path == "/v1/responses" {
								// Native streaming preserves the provider's error unchanged.
								if !strings.Contains(rec.Body.String(), failure.code) {
									t.Fatalf("missing provider code: %s", rec.Body)
								}
							} else if !strings.Contains(rec.Body.String(), "invalid_request_error") {
								t.Fatalf("missing client error type: %s", rec.Body)
							}
							if !streaming && strings.Contains(rec.Body.String(), "private-prompt") {
								t.Fatalf("buffered error leaked response contents: %s", rec.Body)
							}
							if strings.Contains(rec.Body.String(), "message_stop") || strings.Contains(rec.Body.String(), "[DONE]") || strings.Contains(rec.Body.String(), "response.completed") {
								t.Fatalf("failed response completed normally: %s", rec.Body)
							}
							errors := s.stats.RecentUpstreamErrors()
							if len(errors) != 1 || !strings.Contains(errors[0].Message, failure.message) {
								t.Fatalf("failure summary = %+v", errors)
							}
							models := s.stats.snapshot()
							if len(models) != 1 || models[0].Requests != 1 || models[0].Successes != 0 {
								t.Fatalf("failed stream count = %+v", models)
							}
						})
					}
				}
			}
		}
	}
}

func TestCodexStrictOutputBoundRejectsBeforeUpstream(t *testing.T) {
	clients := []struct{ path, fields string }{
		{"/v1/messages", `"max_tokens":128,"messages":[{"role":"user","content":"Hello"}]`},
		{"/v1/chat/completions", `"max_tokens":128,"messages":[{"role":"user","content":"Hello"}]`},
		{"/v1/responses", `"max_output_tokens":128,"input":"Hello"`},
	}
	for _, client := range clients {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", client.path, streaming), func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
				defer upstream.Close()
				b := codex.New(upstream.URL, codexErrorToken{})
				b.HTTP = upstream.Client()
				fallback := newNamedScripted("second", backend.KindOpenAIResponses, step{resp: sseResponse("text/event-stream", "data: [DONE]\n\n")})
				s := newFallbackServer(t, b, fallback, config.BackendConfig{Type: "codex"}, map[string]config.ModelRoute{"m1": {Backend: "codex", Model: "gpt-6-luna", Fallbacks: []config.FallbackRoute{{Backend: "second", Model: "fallback-model"}}}})
				body := fmt.Sprintf(`{"model":"m1","stream":%t,%s}`, streaming, client.fields)
				r := httptest.NewRequest(http.MethodPost, client.path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-LLM-Proxy-Require-Output-Bound", "true")
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, r)
				if rec.Code != http.StatusBadRequest || calls.Load() != 0 || fallback.callCount() != 0 || !strings.Contains(rec.Body.String(), "cannot enforce") {
					t.Fatalf("status=%d upstream=%d fallback=%d body=%s", rec.Code, calls.Load(), fallback.callCount(), rec.Body.String())
				}
			})
		}
	}
}
