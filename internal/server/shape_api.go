package server

import (
	"net/http"
)

// handleShapes serves GET /api/shapes with the retained attempts' conversion
// fingerprints (newest first). Each entry carries identity plus per-hop diff
// counts; GET /api/shapes/{id} returns the full key/type trees.
func (s *Server) handleShapes(w http.ResponseWriter, r *http.Request) {
	requests, err := s.stats.recentRequests(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shared request history unavailable"})
		return
	}
	shapes := make([]ShapeSummary, 0, len(requests))
	for _, req := range requests {
		if req.Shapes == nil {
			continue
		}
		shapes = append(shapes, shapeSummary(req))
	}
	writeJSON(w, http.StatusOK, map[string]any{"shapes": shapes})
}

// handleShape serves GET /api/shapes/{id} with one attempt's full ShapeTrace:
// request and response key/type trees on both sides of the conversion, plus
// the structural diff at each hop.
func (s *Server) handleShape(w http.ResponseWriter, r *http.Request) {
	req, ok, err := s.stats.request(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shared request history unavailable"})
		return
	}
	if !ok || req.Shapes == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "shape not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":               req.ID,
		"at":               req.At,
		"proxy_request_id": req.ProxyRequestID,
		"backend":          req.Backend,
		"model":            req.Model,
		"kind":             req.Kind,
		"status":           req.Status,
		"shapes":           req.Shapes,
	})
}
