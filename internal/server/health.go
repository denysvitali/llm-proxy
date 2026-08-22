package server

import (
	"net/http"
	"sort"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, len(s.backends))
	for _, b := range s.backends {
		if s.enabled(b.Name()) {
			names = append(names, b.Name())
		}
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "backends": names})
}
