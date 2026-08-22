package server

import "net/http"

// handleMessages serves POST /v1/messages (Anthropic Messages API).
// OWNER: agent-anthropic-handler — replace this stub.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	writeAnthropicError(w, http.StatusNotImplemented, "api_error", "not implemented")
}

// handleCountTokens serves POST /v1/messages/count_tokens.
// OWNER: agent-anthropic-handler — replace this stub.
func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	writeAnthropicError(w, http.StatusNotImplemented, "api_error", "not implemented")
}
