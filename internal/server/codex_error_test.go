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
	const message = "Your input exceeds the context window of this model. Please adjust your input and try again."
	clients := []struct{ path, fields string }{
		{"/v1/messages", `"max_tokens":128,"messages":[{"role":"user","content":"Hello"}]`},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"Hello"}]`},
		{"/v1/responses", `"input":"Hello"`},
	}
	for _, client := range clients {
		for _, streaming := range []bool{false, true} {
			for _, started := range []bool{false, true} {
				for _, eventType := range []string{"response.failed", "error"} {
					t.Run(fmt.Sprintf("%s/stream=%t/started=%t/%s", client.path, streaming, started, eventType), func(t *testing.T) {
						var calls atomic.Int32
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							w.Header().Set("Content-Type", "text/event-stream")
							if started {
								_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-6-luna\"}}\n\n")
							}
							errObj := map[string]any{"type": "invalid_request_error", "code": "context_length_exceeded", "message": message}
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
						s := newTestServer(t, []backend.Backend{b}, config.BackendConfig{Type: "codex"})
						body := fmt.Sprintf(`{"model":"codex/gpt-6-luna","stream":%t,%s}`, streaming, client.fields)
						rec := postOpenAI(t, s, client.path, body)
						wantStatus := http.StatusBadRequest
						if streaming && (started || client.path == "/v1/responses") {
							wantStatus = http.StatusOK // Headers are already committed.
						}
						if rec.Code != wantStatus || calls.Load() != 1 {
							t.Fatalf("status=%d want=%d calls=%d body=%s", rec.Code, wantStatus, calls.Load(), rec.Body)
						}
						if !strings.Contains(rec.Body.String(), message) || !strings.Contains(rec.Body.String(), "invalid_request_error") {
							t.Fatalf("missing provider error: %s", rec.Body)
						}
						if !streaming && strings.Contains(rec.Body.String(), "private-prompt") {
							t.Fatalf("buffered error leaked response contents: %s", rec.Body)
						}
						if strings.Contains(rec.Body.String(), "message_stop") || strings.Contains(rec.Body.String(), "[DONE]") || strings.Contains(rec.Body.String(), "response.completed") {
							t.Fatalf("failed response completed normally: %s", rec.Body)
						}
						errors := s.stats.RecentUpstreamErrors()
						if len(errors) != 1 || !strings.Contains(errors[0].Message, message) {
							t.Fatalf("failure summary = %+v", errors)
						}
						models := s.stats.snapshot()
						if len(models) != 1 || models[0].Successes != 0 {
							t.Fatalf("failed stream counted as success: %+v", models)
						}
					})
				}
			}
		}
	}
}
