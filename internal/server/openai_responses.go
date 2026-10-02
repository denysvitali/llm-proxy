package server

import (
	"net/http"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// handleResponses serves POST /v1/responses (OpenAI Responses API, e.g.
// Codex). The translation matrix picks how the request reaches its backend —
// natively when the backend speaks Responses, translated to chat or Anthropic
// Messages otherwise — and exchange relays the response back in Responses
// shape either way.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.handleOpenAIRequest(w, r, backend.KindOpenAIResponses, openAIResponsesDialect())
}
