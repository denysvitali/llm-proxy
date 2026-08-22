package server

import "net/http"

// handleChatCompletions serves POST /v1/chat/completions (OpenAI API).
// OWNER: agent-openai-handlers — replace this stub.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented, "api_error", "not implemented")
}
