package server

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// writeOpenAIModelNotFound answers the canonical OpenAI 404 for a model that
// no configured backend can serve.
func writeOpenAIModelNotFound(w http.ResponseWriter, model string) {
	writeJSON(w, http.StatusNotFound, openAIErrorBody{
		Error: openAIErrorObj{
			Message: fmt.Sprintf("model %q has no available backend", model),
			Type:    openAIErrorType(http.StatusNotFound),
			Code:    "model_not_found",
		},
	})
}

// copyUpstreamRequestID forwards the upstream x-request-id / request-id
// headers onto the client response when present.
func copyUpstreamRequestID(dst, src http.Header) {
	for _, name := range []string{"X-Request-Id", "Request-Id"} {
		if v := src.Get(name); v != "" {
			dst.Set(name, v)
		}
	}
}

// copyCodexResponseHeaders preserves the Responses metadata that Codex reads
// from HTTP response headers. In particular, x-codex-turn-state must make the
// round trip or the next turn loses the upstream's sticky-routing state.
// x-codex-* is an intentionally narrow prefix: it covers rate-limit and
// safety metadata while avoiding arbitrary provider headers.
func copyCodexResponseHeaders(dst, src http.Header) {
	copyUpstreamRequestID(dst, src)
	for name, values := range src {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-codex-") && lower != "openai-model" &&
			lower != "x-openai-model" && lower != "x-reasoning-included" &&
			lower != "x-models-etag" {
			continue
		}
		dst.Del(name)
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

// relayOpenAIUpstreamError answers a non-2xx upstream response: forward up to
// maxErrorRelay bytes of the upstream body (with its Content-Type) when there
// is one, otherwise synthesize an OpenAI error with the mapped type.
func relayOpenAIUpstreamError(w http.ResponseWriter, resp *backend.Response) {
	defer func() { _ = resp.Body.Close() }()
	copyUpstreamRetryAfter(w.Header(), resp.Header)
	data, _ := readAll(resp.Body, maxErrorRelay)
	copyUpstreamRequestID(w.Header(), resp.Header)
	if len(bytes.TrimSpace(data)) > 0 {
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(resp.Status)
		_, _ = w.Write(data)
		return
	}
	writeOpenAIError(w, resp.Status, openAIErrorType(resp.Status), "upstream request failed")
}

// handleChatCompletions serves POST /v1/chat/completions (OpenAI API). The
// translation matrix picks how the request reaches its backend — natively
// when the backend speaks chat, translated otherwise — and exchange relays
// the response back in chat-completions shape either way.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.handleOpenAIRequest(w, r, backend.KindOpenAIChat, openAIDialect())
}
