package server

import "net/http"

// handleDashboard serves GET / — status page with routing and client setup.
// OWNER: agent-dashboard — replace this stub.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte("dashboard not implemented"))
}
