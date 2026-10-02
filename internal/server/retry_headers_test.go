package server

import (
	"net/http"
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
					if body != "" && rec.Body.String() != body {
						t.Fatalf("provider error changed: %s", rec.Body.String())
					}
					if upstream.callCount() != 1 {
						t.Fatalf("429 was retried: %d attempts", upstream.callCount())
					}
				})
			}
		}
	}
}
