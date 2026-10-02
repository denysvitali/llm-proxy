package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/denysvitali/llm-proxy/internal/config"
)

func (s *Server) handleGrokUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.grokUsageCache.get(r.Context(), false)
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

func (s *Server) grokUsageMetadata(r *http.Request) usageMetadata {
	configured := false
	for _, bc := range s.cfg.Backends {
		if bc.Type != grokUsageBackendName || !bc.IsEnabled() {
			continue
		}
		configured = true
		break
	}
	if !configured {
		return usageMetadata{}
	}
	if s.accounts.Grok == nil || !s.accounts.Grok.HasSession() {
		return usageMetadata{Configured: true}
	}
	usage, err := s.grokUsageCache.get(r.Context(), false)
	if err != nil {
		return usageMetadata{Configured: true, Available: false, Error: sanitizeUsageError(err)}
	}
	return usageMetadata{Configured: true, Available: usage.Available}
}

func grokUsageBaseURL(backends []config.BackendConfig) string {
	for _, backendConfig := range backends {
		if backendConfig.Type != grokUsageBackendName {
			continue
		}
		if backendConfig.BaseURL != "" {
			return strings.TrimRight(backendConfig.BaseURL, "/")
		}
	}
	return ""
}
