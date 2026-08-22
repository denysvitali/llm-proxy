package server

import "net/http"

// handleResponses serves POST /v1/responses (OpenAI Responses API).
// OWNER: agent-openai-handlers — replace this stub.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented, "api_error", "not implemented")
}
