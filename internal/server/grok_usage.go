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

// grokUsageMetadata is local so an account-service outage cannot delay the
// dashboard shell; the usage card reports read failures itself.
func (s *Server) grokUsageMetadata() usageMetadata {
	for _, bc := range s.cfg.Backends {
		if bc.Type == grokUsageBackendName && bc.IsEnabled() {
			if s.accounts.Grok == nil || !s.accounts.Grok.HasSession() {
				return usageUnavailable.metadata("")
			}
			return usageAvailable.metadata("")
		}
	}
	return usageUnconfigured.metadata("")
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
