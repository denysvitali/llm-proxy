package server

import "net/http"

// handleModels serves GET /v1/models with the merged backend catalogs.
// OWNER: agent-dashboard — replace this stub.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented, "api_error", "not implemented")
}
