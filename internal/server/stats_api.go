package server

import (
	"net/http"
	"time"
)

// handleStats serves GET /stats with the per-model summary as JSON. With
// persistence enabled this aggregates all retained buckets ("all recorded
// history"); otherwise it falls back to the Prometheus-backed counters.
func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": s.stats.snapshot()})
}

// handleStatsErrors serves GET /api/stats/errors with the most recent
// upstream failures (newest first), for the dashboard's error feed.
func (s *Server) handleStatsErrors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"errors": s.stats.RecentUpstreamErrors()})
}

func (s *Server) handleRequests(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"requests": s.stats.RecentRequests()})
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	req, ok := s.stats.Request(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// handleStatsBackendSeries serves GET /api/stats/backends/{backend}?range=...
// with that provider's aggregate history. Omitting model returns provider-wide
// data; supplying it narrows to a single upstream model.
func (s *Server) handleStatsBackendSeries(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "missing required query parameter \"range\"; supported values: 1h, 6h, 24h, 7d",
		})
		return
	}
	backendName := r.PathValue("backend")
	result, _, err := s.stats.seriesAtScope(rng, time.Now(), backendName, r.PathValue("model"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleStatsSeries serves GET /api/stats?range=1h|6h|24h|7d with fleet-wide
// time series. Unknown or missing ranges answer with 400 JSON.
func (s *Server) handleStatsSeries(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing required query parameter \"range\"; supported values: 1h, 6h, 24h, 7d"})
		return
	}
	series, models, err := s.stats.seriesAt(rng, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "series": series})
}
