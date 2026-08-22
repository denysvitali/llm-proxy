package server

import (
	"fmt"
	"net/http"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// handleResponses serves POST /v1/responses (OpenAI Responses API). Only
// backends that natively accept the Responses API are reachable here; there
// is no chat-to-Responses synthesis for chat-only backends, so those answer
// 400 and the client is pointed at /v1/chat/completions instead.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	env, err := decodeOpenAIEnvelope(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON")
		return
	}
	route, found := s.resolve(r.Context(), env.Model)
	if !found {
		writeOpenAIModelNotFound(w, env.Model)
		return
	}

	if !route.backend.Supports(backend.KindOpenAIResponses) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("backend %s does not accept the Responses API for model %q; use /v1/chat/completions",
				route.backend.Name(), env.Model))
		return
	}

	rewritten, err := rewriteModel(body, route.model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON")
		return
	}
	resp, err := s.sendOpenAIRequest(r, route.backend, backend.KindOpenAIResponses, route.model, rewritten, env.Stream)
	if err != nil {
		s.failOpenAIBackend(w, route.backend, err)
		return
	}
	if resp.Status < 200 || resp.Status > 299 {
		relayOpenAIUpstreamError(w, resp)
		return
	}
	relayOpenAIBody(w, resp)
}
