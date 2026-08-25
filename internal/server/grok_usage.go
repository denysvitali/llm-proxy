package server

import (
	"errors"
	"net/http"
)

func (s *Server) handleGrokUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.grokUsage(r.Context(), false)
	if errors.Is(err, errUsageUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": sanitizeUsageError(err)})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, usage)
}

var errUsageUnavailable = errors.New("grok account usage is unavailable")
