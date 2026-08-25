package server

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// These tests cover gateways that answer HTTP 200 with a JSON error object
// instead of the wire format's success shape — the failure mode behind
// "body is JSON but not a Message" reaching clients (2026-08 compaction
// incident, OpenCode Zen behind Cloudflare).

const cloudflareStyleError = `{"error":{"message":"Prompt is too long","type":"invalid_request","code":2002}}`

// TestMessagesErrorBodyUnder200Native covers the native passthrough path:
// an Anthropic backend that answers 200 with an error object must reach the
// client as a clean 502 in its dialect, never verbatim.
func TestMessagesErrorBodyUnder200Native(t *testing.T) {
	upstream := newScripted(backend.KindAnthropic,
		step{resp: jsonResponse(http.StatusOK, cloudflareStyleError)},
	)
	s := newMsgServerWith(t, upstream)

	rec := postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	parsed := decodeAnthropicError(t, rec)
	if parsed.Error.Type != "api_error" || !strings.Contains(parsed.Error.Message, "Prompt is too long") {
		t.Fatalf("expected api_error carrying upstream message, got %+v", parsed.Error)
	}
}

// TestMessagesErrorBodyUnder200Translated covers the translated buffered
// path: FromOpenAI must reject the body and exchange() must answer 502
// instead of fabricating an empty-content Message.
func TestMessagesErrorBodyUnder200Translated(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIChat,
		step{resp: jsonResponse(http.StatusOK, cloudflareStyleError)},
	)
	s := newMsgServerWith(t, upstream)

	rec := postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	parsed := decodeAnthropicError(t, rec)
	if parsed.Error.Type != "api_error" {
		t.Fatalf("expected api_error, got %s", body)
	}
	if strings.Contains(body, `"text":""`) {
		t.Fatalf("error body must not become an empty-content message: %s", body)
	}
}

// TestChatCompletionsErrorBodyUnder200 checks the chat endpoint's dialect.
func TestChatCompletionsErrorBodyUnder200(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIResponses,
		step{resp: jsonResponse(http.StatusOK, `{"error":{"message":"overloaded","type":"server_error"}}`)},
	)
	s := newMsgServerWith(t, upstream)

	rec := postMsg(t, s, "/v1/chat/completions", `{"model":"m1","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "overloaded") {
		t.Fatalf("client error should carry the upstream message: %s", rec.Body.String())
	}
}

// TestResponsesEndpointErrorBodyUnder200 checks the Responses endpoint's
// dialect.
func TestResponsesEndpointErrorBodyUnder200(t *testing.T) {
	upstream := newScripted(backend.KindOpenAIResponses,
		step{resp: jsonResponse(http.StatusOK, `{"error":{"message":"insufficient credits","type":"insufficient_quota"}}`)},
	)
	s := newMsgServerWith(t, upstream)

	rec := postMsg(t, s, "/v1/responses", `{"model":"m1","input":"hi"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "insufficient credits") {
		t.Fatalf("client error should carry the upstream message: %s", rec.Body.String())
	}
}

// TestErrorShapedBodyNarrowness pins the guard's conservative behavior:
// non-JSON and JSON without an error object relay untouched.
func TestErrorShapedBodyNarrowness(t *testing.T) {
	for _, body := range []string{
		fullAnthropicSSE,
		`{"id":"msg_1","type":"message","content":[]}`,
		`not json at all`,
		`[1,2,3]`,
	} {
		if err := errorShapedBody([]byte(body)); err != nil {
			t.Fatalf("errorShapedBody(%q) = %v, want nil", body, err)
		}
	}
	if err := errorShapedBody([]byte(cloudflareStyleError)); err == nil {
		t.Fatalf("errorShapedBody missed an explicit error object")
	}
	// A literal null error key is not an error.
	if err := errorShapedBody([]byte(`{"id":"x","error":null}`)); err != nil {
		t.Fatalf(`errorShapedBody with "error":null = %v, want nil`, err)
	}
}

// jsonResponse builds a 2xx response with application/json content.
func jsonResponse(status int, body string) *backend.Response {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	return &backend.Response{
		Status: status,
		Header: header,
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}
