package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestAllAPIsPreserveRetryAfterOnUpstreamErrors(t *testing.T) {
	for _, api := range []struct {
		path string
		body string
	}{
		{"/v1/messages", `{"model":"m1","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/chat/completions", `{"model":"m1","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/responses", `{"model":"m1","input":"hi"}`},
	} {
		for _, body := range []string{`{"error":{"message":"daily quota exhausted"}}`, ""} {
			for _, retry := range []string{"86400", "Sat, 03 Oct 2026 00:00:00 GMT"} {
				t.Run(api.path+body+retry, func(t *testing.T) {
					resp := unavailableResponse(http.StatusTooManyRequests, body)
					resp.Header.Set("Retry-After", retry)
					upstream := newScripted(backend.KindOpenAIChat, step{resp: resp})
					s := newMsgServerWith(t, upstream)
					rec := postMsg(t, s, api.path, api.body)
					if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != retry {
						t.Fatalf("status = %d, Retry-After = %q, want 429 and %q", rec.Code, rec.Header().Get("Retry-After"), retry)
					}
					// The native wire relays the provider body verbatim; translated
					// wires keep its message inside the client's own envelope.
					switch {
					case body == "":
					case api.path == "/v1/chat/completions":
						if rec.Body.String() != body {
							t.Fatalf("provider error changed: %s", rec.Body.String())
						}
					default:
						if got := upstreamErrorText(rec.Body.Bytes()); got != "daily quota exhausted" {
							t.Fatalf("provider message lost: %s", rec.Body.String())
						}
						if api.path == "/v1/messages" && !strings.Contains(rec.Body.String(), `"type":"error"`) {
							t.Fatalf("not an Anthropic error envelope: %s", rec.Body.String())
						}
					}
					if upstream.callCount() != 1 {
						t.Fatalf("429 was retried: %d attempts", upstream.callCount())
					}
				})
			}
		}
	}
}

// TestTranslatedUpstreamErrorIsReenvelopedAndInspected covers an Anthropic
// client on a chat-wire backend that rejects the request: the client gets an
// Anthropic error carrying the provider's code and message, and the dashboard
// inspection keeps the error plus the relayed body's shape.
func TestTranslatedUpstreamErrorIsReenvelopedAndInspected(t *testing.T) {
	const upstreamBody = `{"error":{"type":"authentication_error","code":"api_key_expired","message":"This API key has expired."}}`
	upstream := newScripted(backend.KindOpenAIChat, step{resp: unavailableResponse(http.StatusUnauthorized, upstreamBody)})
	s := newMsgServerWith(t, upstream)
	rec := postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	want := `{"type":"error","error":{"type":"authentication_error","message":"api_key_expired: This API key has expired."}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
	requests := s.stats.RecentRequests()
	if len(requests) != 1 {
		t.Fatalf("inspected requests = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Error != "api_key_expired: This API key has expired." {
		t.Fatalf("inspected error = %q", req.Error)
	}
	if req.Shapes == nil || !bodyRecorded(req.Shapes.ResponseClient) {
		t.Fatalf("relayed error body shape not recorded: %+v", req.Shapes)
	}
}
